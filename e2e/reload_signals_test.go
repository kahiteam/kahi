//go:build e2e

package e2e

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// Reload on SIGHUP and the API (FUNC-014, FUNC-038) and log reopen on
// SIGUSR2 (FUNC-021, FUNC-039), at the edges: a reload that is running,
// startup, shutdown, a bad file, and rotation under load.

// countLog counts the daemon log lines whose msg is msg.
func (d *orderedDaemon) countLog(msg string) int {
	n := 0
	for _, m := range d.logLines() {
		if m["msg"] == msg {
			n++
		}
	}
	return n
}

func (d *orderedDaemon) waitLog(s string, timeout time.Duration) {
	d.t.Helper()
	for deadline := time.Now().Add(timeout); !d.logContains(s); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			d.t.Fatalf("daemon log lacks %q after %v", s, timeout)
		}
	}
}

func (d *orderedDaemon) writeConfig(body string) {
	d.t.Helper()
	if err := os.WriteFile(filepath.Join(d.dir, "kahi.toml"), []byte(body), 0o644); err != nil {
		d.t.Fatal(err)
	}
}

// A SIGHUP during a running reload queues one more reload, which reads the
// file once the first is done, so an edit made meanwhile is applied; more
// SIGHUPs meanwhile coalesce into it (#136).
func TestReload_QueuedReloadAppliesLateEdit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := startOrderedDaemon(t, 30, termProgram(t, dir, "stub", 300, "0", true, "stopwaitsecs = 3"))
	d.ready("stub")
	base := readFile(t, filepath.Join(d.dir, "kahi.toml"))

	// A spawn-affecting change: the reload stops stub, which ignores TERM
	// and takes its 3 s stopwaitsecs.
	changed := base + "\n[programs.stub.environment]\nCHANGED = \"1\"\n"
	d.writeConfig(changed)
	d.signal(syscall.SIGHUP)
	d.waitOrder("stub-term", 5*time.Second)

	// During that reload: a new program in the file, then five SIGHUPs.
	d.writeConfig(changed + "\n[programs.late]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\n")
	for range 5 {
		d.signal(syscall.SIGHUP)
	}
	waitForState(t, d.client, "late", "RUNNING", 15*time.Second)
	if n := d.countLog("reloading config"); n != 2 {
		t.Errorf("%d reloads, want 2: the running one and one queued for all five SIGHUPs", n)
	}
	// Signals of one kind do not queue in the kernel, so fewer than five
	// may arrive; one queues the reload, any others coalesce into it.
	if n := d.countLog("reload in progress; another one follows it"); n != 1 {
		t.Errorf("%d queued reloads logged, want 1", n)
	}
}

// During shutdown an API reload is refused with 409 and a SIGHUP or SIGUSR2
// is not acted on; the daemon still exits 0.
func TestReload_IgnoredDuringShutdown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := startOrderedDaemon(t, 30, termProgram(t, dir, "stub", 300, "0", true, "stopwaitsecs = 4"))
	d.ready("stub")
	d.writeConfig(readFile(t, filepath.Join(d.dir, "kahi.toml")) + "\n[programs.extra]\ncommand = \"/bin/sleep 300\"\n")

	d.signal(syscall.SIGTERM)
	d.waitLog(`"msg":"shutting down"`, 5*time.Second)
	apiDo(t, d.socket, "POST", "/api/v1/config/reload", "").wantError(t, "reload during shutdown", http.StatusConflict, "daemon is shutting down", "SHUTTING_DOWN")
	d.signal(syscall.SIGHUP)
	d.signal(syscall.SIGUSR2)
	if code := d.waitExit(15 * time.Second); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	after := false
	for _, m := range d.logLines() {
		if m["msg"] == "shutting down" {
			after = true
		}
		if after && (m["msg"] == "reloading config" || m["msg"] == "log files reopened") {
			t.Errorf("acted on a signal during shutdown: %v", m)
		}
	}
}

// A SIGHUP with a broken file is rejected and logged; the running config
// and processes stay.
func TestReload_SIGHUPBadConfigKept(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := startOrderedDaemon(t, 30, termProgram(t, dir, "web", 300, "0", false, ""))
	d.ready("web")
	before, err := getProcessInfo(d.client, "web")
	if err != nil {
		t.Fatal(err)
	}
	d.writeConfig("[programs.web\ncommand = \n")
	d.signal(syscall.SIGHUP)
	d.waitLog(`"msg":"reload failed"`, 5*time.Second)
	if !d.logContains(`"error":"config error: `) {
		t.Errorf("reload failure does not say config error")
	}
	if after, _ := getProcessInfo(d.client, "web"); after.State != "RUNNING" || after.PID != before.PID {
		t.Errorf("web %s pid %d, want RUNNING with pid %d", after.State, after.PID, before.PID)
	}
	if st, err := d.client.Health(); err != nil || st != "ok" {
		t.Errorf("health %q %v after a failed reload", st, err)
	}
}

// A SIGHUP while the daemon loads its config does not end it (#136). The
// config is a FIFO: the test's open for writing returns once the daemon has
// opened it for reading, so the SIGHUPs arrive mid-startup, not while the
// Go runtime itself is still starting (no program can ignore those).
func TestReload_SIGHUPDuringStartup(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	socket := filepath.Join(dir, "kahi.sock")
	cfg := filepath.Join(dir, "kahi.toml")
	if err := syscall.Mkfifo(cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(kahiBinary, "daemon", "-c", cfg)
	cmd.Dir = dir
	out := daemonOutput(t)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = ctl.NewUnixClient(socket).Shutdown()
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
	})

	w, err := os.OpenFile(cfg, os.O_WRONLY, 0) // returns once the daemon reads
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case err := <-exited:
		exited <- err
		t.Fatalf("daemon ended by a SIGHUP during startup: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	body := fmt.Sprintf("[supervisor]\nshutdown_timeout = 5\n\n[server.unix]\nfile = %q\n\n[programs.web]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\n", socket)
	if _, err := w.WriteString(body); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	waitForSocket(t, socket, 10*time.Second)
	client := ctl.NewUnixClient(socket)
	waitForHealth(t, client, 3*time.Second)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
}

// numprocs 2 -> 4 is a spawn-affecting change: the program is recreated,
// all four instances run and the original two restart (FUNC-014, #119).
func TestReload_NumprocsGrowRecreates(t *testing.T) {
	t.Parallel()
	prog := func(n int) string {
		return fmt.Sprintf("[programs.w]\ncommand = \"/bin/sleep 300\"\nnumprocs = %d\nstartsecs = 0\nstopwaitsecs = 2\n", n)
	}
	client, socket := startDaemon(t, prog(2))
	pids := map[string]int{}
	for _, name := range []string{"w_0", "w_1"} {
		waitForState(t, client, name, "RUNNING", 5*time.Second)
		info, _ := getProcessInfo(client, name)
		pids[name] = info.PID
	}
	rewriteConfig(t, socket, prog(4))
	diff, err := client.Reload()
	if err != nil || fmt.Sprint(diff["changed"]) != "[w]" {
		t.Fatalf("reload: %v %v, want w changed", diff, err)
	}
	for _, name := range []string{"w_0", "w_1", "w_2", "w_3"} {
		waitForState(t, client, name, "RUNNING", 10*time.Second)
	}
	for name, old := range pids {
		if info, _ := getProcessInfo(client, name); info.PID == old {
			t.Errorf("%s kept pid %d, want it restarted", name, old)
		}
		if err := syscall.Kill(old, 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("old %s (pid %d) still exists: %v", name, old, err)
		}
	}
}

// SIGUSR2 reopens every log file: stdout and stderr logs of all programs
// write to new files at their paths and the renamed ones stop growing.
func TestLog_SIGUSR2ReopensAllFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	both := writeScript(t, dir, "both.sh", "while :; do echo out; echo err >&2; sleep 0.1; done")
	single := writeScript(t, dir, "single.sh", "while :; do echo tick; sleep 0.1; done")
	logs := map[string]string{
		"a.out": filepath.Join(dir, "a.out"), "a.err": filepath.Join(dir, "a.err"), "b.out": filepath.Join(dir, "b.out"),
	}
	client, _ := startDaemon(t, fmt.Sprintf(`[programs.a]
command = %q
startsecs = 0
stopwaitsecs = 2
stdout_logfile = %q
stderr_logfile = %q

[programs.b]
command = %q
startsecs = 0
stopwaitsecs = 2
stdout_logfile = %q
`, both, logs["a.out"], logs["a.err"], single, logs["b.out"]))
	for _, p := range logs {
		waitFor(t, p, func() string { b, _ := os.ReadFile(p); return string(b) }, "\n")
	}
	for _, p := range logs {
		if err := os.Rename(p, p+".1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Kill(daemonPID(t, client), syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	for _, p := range logs {
		waitFor(t, p, func() string { b, _ := os.ReadFile(p); return string(b) }, "\n")
	}
	sizes := map[string]int64{}
	for name, p := range logs {
		fi, _ := os.Stat(p + ".1")
		sizes[name] = fi.Size()
	}
	time.Sleep(500 * time.Millisecond)
	for name, p := range logs {
		if fi, _ := os.Stat(p + ".1"); fi.Size() != sizes[name] {
			t.Errorf("%s.1 still growing after SIGUSR2", name)
		}
	}
}

// Rotations while a program writes fast lose and duplicate no line: every
// line is in exactly one of the rotated files or the current one.
func TestLog_SIGUSR2NoLossDuringRotation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "seq.log")
	seq := writeScript(t, dir, "seq.sh", "i=0\nwhile [ $i -lt 600 ]; do echo $i; i=$((i+1)); sleep 0.005; done\nexec sleep 300")
	client, _ := startDaemon(t, fmt.Sprintf("[programs.seq]\ncommand = %q\nstartsecs = 0\nstopwaitsecs = 2\nstdout_logfile = %q\n", seq, logPath))
	waitFor(t, "seq.log", func() string { b, _ := os.ReadFile(logPath); return string(b) }, "\n")
	pid := daemonPID(t, client)
	for i := 1; i <= 4; i++ {
		time.Sleep(300 * time.Millisecond)
		if err := os.Rename(logPath, fmt.Sprintf("%s.%d", logPath, i)); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(pid, syscall.SIGUSR2); err != nil {
			t.Fatal(err)
		}
	}
	lastLine := regexp.MustCompile(`(?m)^599$`)
	files := []string{logPath, logPath + ".1", logPath + ".2", logPath + ".3", logPath + ".4"}
	all := func() string {
		var b strings.Builder
		for _, f := range files {
			data, _ := os.ReadFile(f)
			b.Write(data)
		}
		return b.String()
	}
	for deadline := time.Now().Add(20 * time.Second); !lastLine.MatchString(all()); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("writer did not reach 599")
		}
	}
	var nums []int
	for _, line := range strings.Fields(all()) {
		n, err := strconv.Atoi(line)
		if err != nil {
			t.Fatalf("unexpected line %q", line)
		}
		nums = append(nums, n)
	}
	sort.Ints(nums)
	if len(nums) != 600 {
		t.Fatalf("%d lines across the rotated files, want 600", len(nums))
	}
	for i, n := range nums {
		if n != i {
			t.Fatalf("line %d missing or duplicated", i)
		}
	}
}

// SIGUSR2 touches only files: a program's console output keeps flowing, and
// a daemon with no log files at all acknowledges it without error.
func TestLog_SIGUSR2ConsoleAndNoLogFiles(t *testing.T) {
	t.Parallel()
	ticker := writeScript(t, t.TempDir(), "ticker.sh", "while :; do echo tick; sleep 0.1; done")
	dir := includeConfig(t, map[string]string{"kahi.toml": "[programs.ticker]\ncommand = \"" + ticker + "\"\nstartsecs = 0\nstopwaitsecs = 2\n"})
	client, output := startIncludeDaemon(t, dir)
	waitFor(t, "daemon output", output, `"log":"tick"`)
	if err := syscall.Kill(daemonPID(t, client), syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "daemon output", output, `"msg":"log files reopened"`)
	at := strings.Count(output(), `"log":"tick"`)
	for deadline := time.Now().Add(5 * time.Second); strings.Count(output(), `"log":"tick"`) < at+3; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("console output stopped after SIGUSR2")
		}
	}
	if strings.Contains(output(), `"level":"ERROR"`) {
		t.Errorf("SIGUSR2 without log files logged an error:\n%s", output())
	}
	if st, err := client.Health(); err != nil || st != "ok" {
		t.Errorf("health %q %v after SIGUSR2", st, err)
	}
}
