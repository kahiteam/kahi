//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// Ordered shutdown by priority level (FUNC-066, FUNC-038, ADR-031, #151).

type orderedDaemon struct {
	t      *testing.T
	cmd    *exec.Cmd
	dir    string
	socket string
	log    string
	order  string
	client *ctl.Client
	exit   chan error
}

// startOrderedDaemon runs a daemon with a log file and the programs, where
// DIR is replaced by its directory and ORDER by a file the programs append
// their signal events to.
func startOrderedDaemon(t *testing.T, timeout int, programs string) *orderedDaemon {
	t.Helper()
	d := &orderedDaemon{t: t, dir: shortTempDir(t)}
	d.socket = filepath.Join(d.dir, "kahi.sock")
	d.log = filepath.Join(d.dir, "kahi.log")
	d.order = filepath.Join(d.dir, "order")
	cfgPath := filepath.Join(d.dir, "kahi.toml")
	programs = strings.ReplaceAll(strings.ReplaceAll(programs, "ORDER", d.order), "DIR", d.dir)
	cfg := fmt.Sprintf("[supervisor]\nlogfile = %q\nshutdown_timeout = %d\n\n[server.unix]\nfile = %q\n\n%s", d.log, timeout, d.socket, programs)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	d.cmd = startDaemonCmd(t, kahiBinary, cfgPath, d.dir)
	d.exit = make(chan error, 1)
	go func() { d.exit <- d.cmd.Wait() }()
	t.Cleanup(func() {
		_ = d.cmd.Process.Kill()
		<-d.exit
	})
	waitForSocket(t, d.socket, 5*time.Second)
	d.client = ctl.NewUnixClient(d.socket)
	waitForHealth(t, d.client, 3*time.Second)
	return d
}

// termProgram returns a program that, on SIGTERM, appends "{name}-term" to
// the daemon's order file, waits delay and appends "{name}-exit" before
// exiting; with ignore it only appends the term line and keeps running. It is
// ready once {name}.ready exists in the daemon's directory.
func termProgram(t *testing.T, dir, name string, priority int, delay string, ignore bool, extra string) string {
	t.Helper()
	// Programs run in the daemon's directory, so the order file and the
	// ready markers are relative paths there.
	handler := "echo " + name + "-term >> order; sleep " + delay + "; echo " + name + "-exit >> order; exit 0"
	if ignore {
		handler = "echo " + name + "-term >> order"
	}
	script := writeScript(t, dir, name+".sh", "trap '"+handler+"' TERM\necho ready > "+name+".ready\nwhile true; do sleep 0.1; done")
	return fmt.Sprintf("[programs.%s]\ncommand = %q\nstartsecs = 0\npriority = %d\n%s\n", name, script, priority, extra)
}

func (d *orderedDaemon) ready(names ...string) {
	d.t.Helper()
	for _, n := range names {
		waitForMarker(d.t, filepath.Join(d.dir, n+".ready"), "ready", 5*time.Second)
	}
}

func (d *orderedDaemon) signal(sig syscall.Signal) {
	d.t.Helper()
	if err := d.cmd.Process.Signal(sig); err != nil {
		d.t.Fatal(err)
	}
}

// waitExit waits for the daemon to exit and returns its exit code.
func (d *orderedDaemon) waitExit(timeout time.Duration) int {
	d.t.Helper()
	select {
	case err := <-d.exit:
		d.exit <- err // keep it for the cleanup
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		if err != nil {
			d.t.Fatalf("daemon wait: %v", err)
		}
		return 0
	case <-time.After(timeout):
		d.t.Fatalf("daemon still running %v after the signal", timeout)
		return -1
	}
}

func (d *orderedDaemon) orderLines() []string {
	b, _ := os.ReadFile(d.order)
	return strings.Fields(string(b))
}

// waitOrder waits until line is in the order file.
func (d *orderedDaemon) waitOrder(line string, timeout time.Duration) {
	d.t.Helper()
	deadline := time.Now().Add(timeout)
	for !contains(d.orderLines(), line) {
		if time.Now().After(deadline) {
			d.t.Fatalf("order %v lacks %s after %v", d.orderLines(), line, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// logLines returns the daemon log lines decoded.
func (d *orderedDaemon) logLines() []map[string]any {
	f, err := os.Open(d.log)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// levels returns the "stopping priority level" log lines as "key:names".
func (d *orderedDaemon) levels() []string {
	var out []string
	for _, m := range d.logLines() {
		msg, _ := m["msg"].(string)
		if key, ok := strings.CutPrefix(msg, "stopping priority level "); ok {
			out = append(out, key+":"+fmt.Sprint(m["processes"]))
		}
	}
	return out
}

func (d *orderedDaemon) logContains(s string) bool {
	b, _ := os.ReadFile(d.log)
	return strings.Contains(string(b), s)
}

func indexOf(lines []string, s string) int {
	for i, l := range lines {
		if l == s {
			return i
		}
	}
	return -1
}

func TestShutdown_OrderedLevels(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := startOrderedDaemon(t, 30, termProgram(t, dir, "app", 300, "1", false, "")+
		termProgram(t, dir, "app2", 300, "1", false, "")+
		termProgram(t, dir, "cache", 200, "1", false, "")+
		termProgram(t, dir, "db", 100, "1", false, ""))
	d.ready("app", "app2", "cache", "db")

	start := time.Now()
	d.signal(syscall.SIGTERM)
	code := d.waitExit(10 * time.Second)
	elapsed := time.Since(start)
	if code != 0 || elapsed < 3*time.Second || elapsed > 5*time.Second {
		t.Errorf("exit %d after %v, want 0 between 3 and 5 s", code, elapsed)
	}

	o := d.orderLines()
	// Both priority-300 programs are signalled before either has exited; each
	// lower level only after the level above has exited.
	if i, j := indexOf(o, "app-term"), indexOf(o, "app2-term"); i < 0 || j < 0 || max(i, j) > 1 {
		t.Errorf("order %v: app and app2 must be signalled first, together", o)
	}
	for _, pair := range [][2]string{{"app-exit", "cache-term"}, {"app2-exit", "cache-term"}, {"cache-exit", "db-term"}} {
		if a, b := indexOf(o, pair[0]), indexOf(o, pair[1]); a < 0 || b < 0 || a > b {
			t.Errorf("order %v: %s must come before %s", o, pair[0], pair[1])
		}
	}
	want := []string{"300:app2,app", "200:cache", "100:db"}
	if got := d.levels(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("level log lines %v, want %v", got, want)
	}
}

// SIGINT and SIGQUIT shut down in the same order and exit 0 (FUNC-038).
func TestShutdown_IntAndQuit(t *testing.T) {
	t.Parallel()
	for name, sig := range map[string]syscall.Signal{"INT": syscall.SIGINT, "QUIT": syscall.SIGQUIT} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			d := startOrderedDaemon(t, 30, termProgram(t, dir, "high", 300, "0.3", false, "")+
				termProgram(t, dir, "low", 100, "0", false, ""))
			d.ready("high", "low")
			d.signal(sig)
			if code := d.waitExit(10 * time.Second); code != 0 {
				t.Errorf("exit %d, want 0", code)
			}
			if o := d.orderLines(); indexOf(o, "high-exit") < 0 || indexOf(o, "high-exit") > indexOf(o, "low-term") {
				t.Errorf("order %v, want high to exit before low is signalled", o)
			}
		})
	}
}

// A level that ignores SIGTERM is SIGKILLed after its stopwaitsecs; the next
// level follows. During shutdown starts are refused, status still answers
// and /healthz reports shutting_down.
func TestShutdown_StubbornLevelThenNext(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := startOrderedDaemon(t, 30, termProgram(t, dir, "stub", 300, "0", true, "stopwaitsecs = 2")+
		termProgram(t, dir, "cache", 200, "0", false, "")+
		"[programs.idle]\ncommand = \"/bin/sleep 300\"\nautostart = false\npriority = 999\n")
	d.ready("stub", "cache")

	start := time.Now()
	d.signal(syscall.SIGTERM)
	d.waitOrder("stub-term", 5*time.Second)

	apiDo(t, d.socket, "POST", "/api/v1/processes/idle/start", "").
		wantError(t, "start during shutdown", http.StatusConflict, "daemon is shutting down", "SHUTTING_DOWN")
	var list []processInfo
	apiDo(t, d.socket, "GET", "/api/v1/processes", "").jsonBody(t, "list during shutdown", http.StatusOK, &list)
	var health map[string]string
	apiDo(t, d.socket, "GET", "/healthz", "").jsonBody(t, "healthz during shutdown", http.StatusServiceUnavailable, &health)
	if health["status"] != "shutting_down" {
		t.Errorf("healthz %v, want shutting_down", health)
	}

	d.waitOrder("cache-term", 10*time.Second)
	if since := time.Since(start); since < 1800*time.Millisecond {
		t.Errorf("cache signalled %v after the TERM, want after stub's 2 s stopwaitsecs", since)
	}
	if code := d.waitExit(10 * time.Second); code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if !d.logContains(`"process":"stub"`) || !d.logContains(`"signal":"SIGKILL"`) {
		t.Errorf("log does not show stub killed by SIGKILL")
	}
}

// shutdown_timeout covers all levels: at the deadline everything still
// running is SIGKILLed, including levels never signalled.
func TestShutdown_TimeoutKillsAllLevels(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := startOrderedDaemon(t, 2, termProgram(t, dir, "a", 300, "1.5", false, "")+
		termProgram(t, dir, "b", 200, "1.5", false, "")+
		termProgram(t, dir, "c", 100, "1.5", false, ""))
	d.ready("a", "b", "c")
	c, err := getProcessInfo(d.client, "c")
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	d.signal(syscall.SIGTERM)
	code := d.waitExit(10 * time.Second)
	if elapsed := time.Since(start); code != 0 || elapsed < 2*time.Second || elapsed > 3500*time.Millisecond {
		t.Errorf("exit %d after %v, want 0 between 2 and 3.5 s", code, elapsed)
	}
	if o := d.orderLines(); contains(o, "c-term") || !contains(o, "b-term") {
		t.Errorf("order %v: b must be signalled and c never", o)
	}
	if err := syscall.Kill(c.PID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("c (pid %d) still exists after shutdown: %v", c.PID, err)
	}
}

// A second SIGTERM, SIGINT or SIGQUIT kills every remaining process at once.
func TestShutdown_SecondSignalKills(t *testing.T) {
	t.Parallel()
	for name, sig := range map[string]syscall.Signal{"TERM": syscall.SIGTERM, "INT": syscall.SIGINT, "QUIT": syscall.SIGQUIT} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			d := startOrderedDaemon(t, 30, termProgram(t, dir, "stub", 300, "0", true, "stopwaitsecs = 20"))
			d.ready("stub")
			info, _ := getProcessInfo(d.client, "stub")
			d.signal(syscall.SIGTERM)
			d.waitOrder("stub-term", 5*time.Second)
			start := time.Now()
			d.signal(sig)
			code := d.waitExit(5 * time.Second)
			if elapsed := time.Since(start); code != 0 || elapsed > 1500*time.Millisecond {
				t.Errorf("exit %d %v after the second signal, want 0 within about 1 s", code, elapsed)
			}
			if err := syscall.Kill(info.PID, 0); !errors.Is(err, syscall.ESRCH) {
				t.Errorf("stub (pid %d) still exists: %v", info.PID, err)
			}
		})
	}
}

// STOPPED, EXITED and FATAL processes are skipped; STARTING is stopped with
// its stop signal and BACKOFF moves to STOPPED; nothing is restarted once
// shutdown began.
func TestShutdown_StatesAndNoRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	again := writeScript(t, dir, "again.sh", "sleep 1\nexit 0")
	d := startOrderedDaemon(t, 30, termProgram(t, dir, "slow", 300, "3", false, "")+`
[programs.stopped]
command = "/bin/sleep 300"
autostart = false
priority = 200

[programs.exited]
command = "/bin/sh -c 'exit 0'"
startsecs = 0
autorestart = "false"
priority = 200

[programs.fatal]
command = "/nonexistent/kahi-e2e"
startretries = 0
priority = 200

[programs.starting]
command = "/bin/sleep 300"
startsecs = 60
priority = 200

[programs.backoff]
command = "/bin/sh -c 'exit 1'"
startsecs = 1
startretries = 9
priority = 300

[programs.again]
command = "`+again+`"
startsecs = 0
autorestart = "true"
priority = 100
`)
	d.ready("slow")
	waitForState(t, d.client, "exited", "EXITED", 5*time.Second)
	waitForState(t, d.client, "fatal", "FATAL", 5*time.Second)
	if info, _ := getProcessInfo(d.client, "starting"); info.State != "STARTING" {
		t.Fatalf("starting is %s, want STARTING", info.State)
	}
	// Signal right after backoff is seen in BACKOFF, within its retry delay.
	waitForState(t, d.client, "backoff", "BACKOFF", 5*time.Second)

	d.signal(syscall.SIGTERM)
	if code := d.waitExit(15 * time.Second); code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	// The daemon logs every spawn: none of again after shutdown began.
	shutting := false
	for _, m := range d.logLines() {
		if m["msg"] == "shutting down" {
			shutting = true
		}
		if shutting && m["msg"] == "started" && m["process"] == "again" {
			t.Errorf("again was spawned after shutdown began: %v", m)
		}
	}
	if !shutting {
		t.Fatal("no 'shutting down' log line")
	}
	levels := strings.Join(d.levels(), " ")
	for _, skipped := range []string{"stopped", "exited", "fatal"} {
		if strings.Contains(levels, skipped) {
			t.Errorf("level lines %q name %s, which was not running", levels, skipped)
		}
	}
	// backoff shares the first level, so it is stopped at once, without a
	// signal; in a later level its pending retry would already have moved it
	// to STOPPED.
	if !strings.HasPrefix(levels, "300:slow,backoff ") || !strings.Contains(levels, "200:starting") {
		t.Errorf("level lines %q, want 300:slow,backoff then 200:starting", levels)
	}
	if !d.logContains(`"process":"starting","exit_code":-1,"signal":"SIGTERM"`) {
		t.Errorf("log does not show starting stopped by SIGTERM")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

// A [groups] table's priority puts its members in their own, first level.
func TestShutdown_TableLevelFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := startOrderedDaemon(t, 30, termProgram(t, dir, "t1", 999, "0", false, "")+
		termProgram(t, dir, "t2", 999, "0", false, "")+
		termProgram(t, dir, "solo", 300, "0", false, "")+
		"[groups.tbl]\nprograms = [\"t1\", \"t2\"]\npriority = 400\n")
	d.ready("t1", "t2", "solo")
	d.signal(syscall.SIGTERM)
	if code := d.waitExit(10 * time.Second); code != 0 {
		t.Errorf("exit %d", code)
	}
	if got := strings.Join(d.levels(), " "); got != "400/999:t2,t1 300:solo" {
		t.Errorf("levels %q, want 400/999:t2,t1 then 300:solo", got)
	}
	if o := d.orderLines(); indexOf(o, "solo-term") < max(indexOf(o, "t1-exit"), indexOf(o, "t2-exit")) {
		t.Errorf("order %v, want the table to stop before solo", o)
	}
}

// The per-level sum warning at load and on reload replaces the
// per-program one.
func TestShutdown_SumWarning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	programs := termProgram(t, dir, "a", 300, "0", false, "stopwaitsecs = 20") +
		termProgram(t, dir, "b", 200, "0", false, "stopwaitsecs = 20")
	want := "ordered shutdown can take up to 40s (largest stopwaitsecs per priority level, summed), more than supervisor.shutdown_timeout (30s); processes still running then are SIGKILLed"

	// At load the warning goes to the daemon's stderr.
	sdir := shortTempDir(t)
	cfg := filepath.Join(sdir, "kahi.toml")
	body := fmt.Sprintf("[supervisor]\nshutdown_timeout = 30\n\n[server.unix]\nfile = %q\n\n%s", filepath.Join(sdir, "k.sock"), programs)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr lockedBuffer
	cmd := exec.Command(kahiBinary, "daemon", "-c", cfg)
	cmd.Dir = sdir
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForSocket(t, filepath.Join(sdir, "k.sock"), 5*time.Second)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if !strings.Contains(stderr.String(), "warning: "+want) {
		t.Errorf("load stderr %q lacks the warning", stderr.String())
	}

	// On reload it comes back to ctl.
	d := startOrderedDaemon(t, 30, programs)
	_, errOut, code := runCtl(t, d.socket, "reload")
	if code != 0 || !strings.Contains(errOut, "warning: "+want) {
		t.Errorf("reload: exit %d, stderr %q; want the warning", code, errOut)
	}
	if strings.Contains(stderr.String()+errOut, "exceeds supervisor.shutdown_timeout") {
		t.Error("the old per-program warning still appears")
	}
}

// kahi init, the sample config and the README explain how to tune shutdown;
// the default timeout is 30 s.
func TestShutdown_TuningDocumented(t *testing.T) {
	t.Parallel()
	initOut, _, code := runKahi(t, t.TempDir(), "init", "--stdout")
	if code != 0 {
		t.Fatalf("kahi init: exit %d", code)
	}
	sample, _ := os.ReadFile("../cmd/kahi/kahi.toml")
	readme, _ := os.ReadFile("../README.md")
	for name, text := range map[string]string{"kahi init": initOut, "kahi.toml": string(sample), "README": string(readme)} {
		for _, want := range []string{"# shutdown_timeout = 30 ", "largest stopwaitsecs in each level", "stop together", "holds up the next"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	}
}

// SIGUSR2 then SIGTERM are handled in order; a SIGHUP during a running
// reload is ignored with a log line (signal-queuing).
func TestShutdown_SignalQueue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := startOrderedDaemon(t, 30, termProgram(t, dir, "stub", 300, "0", true, "stopwaitsecs = 3"))
	d.ready("stub")

	// A spawn-affecting change makes the reload stop stub, which takes its
	// 3 s stopwaitsecs; a second SIGHUP meanwhile is ignored.
	cfg := readFile(t, filepath.Join(d.dir, "kahi.toml")) + "\n[programs.stub.environment]\nCHANGED = \"1\"\n"
	if err := os.WriteFile(filepath.Join(d.dir, "kahi.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	d.signal(syscall.SIGHUP)
	d.waitOrder("stub-term", 5*time.Second)
	d.signal(syscall.SIGHUP)
	deadline := time.Now().Add(5 * time.Second)
	for !d.logContains("reload already in progress; ignoring SIGHUP") {
		if time.Now().After(deadline) {
			t.Fatal("second SIGHUP during the reload was not reported")
		}
		time.Sleep(50 * time.Millisecond)
	}

	d.signal(syscall.SIGUSR2)
	deadline = time.Now().Add(5 * time.Second)
	for !d.logContains("log files reopened") {
		if time.Now().After(deadline) {
			t.Fatal("SIGUSR2 not handled")
		}
		time.Sleep(50 * time.Millisecond)
	}
	d.signal(syscall.SIGTERM)
	if code := d.waitExit(15 * time.Second); code != 0 {
		t.Errorf("exit %d", code)
	}
	var seq []string
	for _, m := range d.logLines() {
		switch m["msg"] {
		case "received signal":
			seq = append(seq, fmt.Sprint(m["signal"]))
		case "log files reopened", "shutting down":
			seq = append(seq, fmt.Sprint(m["msg"]))
		}
	}
	got := strings.Join(seq, "|")
	if !strings.Contains(got, "SIGUSR2|log files reopened|SIGTERM|shutting down") {
		t.Errorf("signal handling sequence %q, want SIGUSR2, the reopen, SIGTERM, then shutdown", got)
	}
}
