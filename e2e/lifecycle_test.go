//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Process lifecycle (FUNC-001..004): each test drives the real daemon and
// observes the child from outside, through status, files the child writes
// and the kernel.

// waitForLines polls path until it holds at least n lines.
func waitForLines(t *testing.T, path string, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if countLines(path) >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s has %d lines after %v, want >= %d", path, countLines(path), timeout, n)
}

// readPIDFile polls path until it holds a PID.
func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no PID in %s", path)
	return 0
}

// alive reports whether pid exists.
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// waitForDeath polls until pid is gone.
func waitForDeath(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d still alive after %v", pid, timeout)
}

// postAPI sends POST path over the Unix socket and returns status and body;
// the ctl client hides the status code.
func postAPI(t *testing.T, socketPath, path string) (int, string) {
	t.Helper()
	hc := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}
	resp, err := hc.Post("http://unix"+path, "application/json", nil)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// A process is RUNNING only after startsecs: it is STARTING before.
func TestLifecycle_StartsecsLowerBound(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.slow]
command = "/bin/sleep 300"
autostart = false
startsecs = 2
`)
	start := time.Now()
	if err := client.Start("slow"); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(time.Second)
	if info, err := getProcessInfo(client, "slow"); err != nil || info.State != "STARTING" {
		t.Fatalf("after 1s: state %q (err %v), want STARTING", info.State, err)
	}
	waitForState(t, client, "slow", "RUNNING", 5*time.Second)
	if elapsed := time.Since(start); elapsed < 2*time.Second {
		t.Fatalf("RUNNING after %v, want >= startsecs (2s)", elapsed)
	}
}

// A program that never reaches RUNNING is spawned startretries+1 times, then
// FATAL. autorestart does not apply before RUNNING: autorestart = true does
// not add spawns.
func TestLifecycle_RetriesToFatal(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := writeScript(t, dir, "fail.sh", "echo run >> "+runs+"\nexit 1")

	client, _ := startDaemon(t, `
[programs.fail]
command = "`+script+`"
autostart = true
autorestart = "true"
startsecs = 1
startretries = 2
`)
	waitForState(t, client, "fail", "FATAL", 15*time.Second)
	time.Sleep(2 * time.Second)
	if got := countLines(runs); got != 3 {
		t.Fatalf("spawned %d times, want startretries+1 = 3", got)
	}
	if info, _ := getProcessInfo(client, "fail"); info.State != "FATAL" {
		t.Fatalf("state %q 2s after FATAL, want it to stay FATAL", info.State)
	}
}

// Reaching RUNNING resets the retry count: with startretries = 1, a run that
// fails, one that reaches RUNNING and exits, then two failures is 4 spawns.
// Without the reset the third spawn would already be FATAL.
func TestLifecycle_RetryCountResetsAfterRunning(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := writeScript(t, dir, "flaky.sh", fmt.Sprintf(`echo run >> %s
if [ "$(wc -l < %s)" -eq 2 ]; then sleep 2; fi
exit 1`, runs, runs))

	client, _ := startDaemon(t, `
[programs.flaky]
command = "`+script+`"
autostart = true
autorestart = "true"
startsecs = 1
startretries = 1
`)
	waitForState(t, client, "flaky", "FATAL", 20*time.Second)
	time.Sleep(time.Second)
	if got := countLines(runs); got != 4 {
		t.Fatalf("spawned %d times, want 4 (retry count reset by RUNNING)", got)
	}
}

// Backoff delays double: 1s, 2s, 4s between attempts.
func TestLifecycle_BackoffDelaysDouble(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := writeScript(t, dir, "fail.sh", "echo run >> "+runs+"\nexit 1")

	client, _ := startDaemon(t, `
[programs.fail]
command = "`+script+`"
autostart = false
startsecs = 1
startretries = 3
`)
	if err := client.Start("fail"); err != nil {
		t.Fatalf("start: %v", err)
	}
	var spawns []time.Time
	deadline := time.Now().Add(15 * time.Second)
	for len(spawns) < 4 && time.Now().Before(deadline) {
		if countLines(runs) > len(spawns) {
			spawns = append(spawns, time.Now())
			continue
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(spawns) != 4 {
		t.Fatalf("saw %d spawns, want 4", len(spawns))
	}
	for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		gap := spawns[i+1].Sub(spawns[i])
		if gap < want-100*time.Millisecond || gap > want+800*time.Millisecond {
			t.Errorf("gap %d = %v, want about %v", i+1, gap, want)
		}
	}
	waitForState(t, client, "fail", "FATAL", 5*time.Second)
}

// The backoff delay stops doubling at 60s: the seventh delay is 60s, not
// 64s. The test sits through 1+2+4+8+16+32+60s of backoff.
func TestLifecycle_BackoffCappedAt60s(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := writeScript(t, dir, "fail.sh", "echo run >> "+runs+"\nexit 1")

	client, _ := startDaemon(t, `
[programs.fail]
command = "`+script+`"
autostart = false
startsecs = 1
startretries = 7
`)
	if err := client.Start("fail"); err != nil {
		t.Fatalf("start: %v", err)
	}
	var spawns []time.Time
	deadline := time.Now().Add(150 * time.Second)
	for len(spawns) < 8 && time.Now().Before(deadline) {
		if countLines(runs) > len(spawns) {
			spawns = append(spawns, time.Now())
			continue
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(spawns) != 8 {
		t.Fatalf("saw %d spawns, want 8", len(spawns))
	}
	for i, want := range []time.Duration{32 * time.Second, 60 * time.Second} {
		gap := spawns[6+i].Sub(spawns[5+i])
		if gap < want-500*time.Millisecond || gap > want+time.Second {
			t.Errorf("gap %d = %v, want about %v", 6+i, gap, want)
		}
	}
	waitForState(t, client, "fail", "FATAL", 5*time.Second)
}

// A stop shows STOPPING until the child is gone; a second stop meanwhile is
// ignored; a stop of a stopped process is an error.
func TestLifecycle_StoppingVisible(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	script := writeScript(t, dir, "stubborn.sh", "trap '' TERM\necho ready > "+ready+"\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.stubborn]
command = "`+script+`"
autostart = true
startsecs = 0
stopwaitsecs = 3
`)
	waitForState(t, client, "stubborn", "RUNNING", 5*time.Second)
	waitForMarker(t, ready, "ready", 5*time.Second)
	if err := client.Stop("stubborn"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForState(t, client, "stubborn", "STOPPING", 2*time.Second)
	if err := client.Stop("stubborn"); err != nil {
		t.Fatalf("second stop during STOPPING: %v, want it ignored", err)
	}
	waitForState(t, client, "stubborn", "STOPPED", 10*time.Second)
	err := client.Stop("stubborn")
	if err == nil || !strings.Contains(err.Error(), "process not running: stubborn") {
		t.Fatalf("stop of stopped process: err %v, want \"process not running: stubborn\"", err)
	}
}

// A child that exits on its own while STOPPING ends STOPPED, not EXITED: a
// stop was requested, so the exit completes it (#177).
func TestLifecycle_ExitDuringStoppingIsStopped(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	script := writeScript(t, dir, "late.sh", "trap '' TERM\necho ready > "+ready+"\nsleep 2\nexit 3")

	client, _ := startDaemon(t, `
[programs.late]
command = "`+script+`"
autostart = false
startsecs = 0
stopwaitsecs = 20
`)
	if err := client.Start("late"); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForState(t, client, "late", "RUNNING", 5*time.Second)
	waitForMarker(t, ready, "ready", 5*time.Second)
	start := time.Now()
	if err := client.Stop("late"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForState(t, client, "late", "STOPPING", 2*time.Second)
	waitForState(t, client, "late", "STOPPED", 10*time.Second)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("STOPPED after %v: the child's own exit did not end the stop", elapsed)
	}
}

// The command is exec'd without a shell, in its own process group.
func TestLifecycle_ExecWithoutShell(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.echo]
command = "/bin/echo a;b *"
autostart = true
startsecs = 0
autorestart = "false"

[programs.sleeper]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	out := waitForOutput(t, client, "echo", "a;b *")
	if !strings.Contains(out, "a;b *\n") {
		t.Fatalf("echo printed %q, want the arguments verbatim (no shell)", out)
	}

	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)
	info, err := getProcessInfo(client, "sleeper")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := exec.Command("ps", "-o", "pgid=,comm=", "-p", strconv.Itoa(info.PID)).Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	fields := strings.Fields(string(ps))
	if len(fields) != 2 {
		t.Fatalf("ps output %q", ps)
	}
	if fields[0] != strconv.Itoa(info.PID) {
		t.Errorf("pgid %s, want the child's own pid %d", fields[0], info.PID)
	}
	if filepath.Base(fields[1]) != "sleep" {
		t.Errorf("comm %q, want sleep (exec'd directly)", fields[1])
	}
	daemonPgid, err := syscall.Getpgid(daemonPID(t, client))
	if err != nil {
		t.Fatal(err)
	}
	if strconv.Itoa(daemonPgid) == fields[0] {
		t.Errorf("child shares the daemon's process group %d", daemonPgid)
	}
}

// Every child gets SUPERVISOR_ENABLED, SUPERVISOR_PROCESS_NAME and
// SUPERVISOR_GROUP_NAME, also with clean_environment.
func TestLifecycle_SupervisorEnv(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "env.sh", "echo \"E=$SUPERVISOR_ENABLED P=$SUPERVISOR_PROCESS_NAME G=$SUPERVISOR_GROUP_NAME\"\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.web]
command = "`+script+`"
autostart = true
startsecs = 0
clean_environment = true
`)
	waitForOutput(t, client, "web", "E=1 P=web G=web")
}

// A process that cannot be spawned is FATAL and its status says why.
func TestLifecycle_SpawnErrors(t *testing.T) {
	dir := t.TempDir()
	notExec := filepath.Join(dir, "notexec")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")

	client, _ := startDaemon(t, `
[programs.nobinary]
command = "`+missing+`"
autostart = true
startretries = 0

[programs.noexec]
command = "`+notExec+`"
autostart = true
startretries = 0

[programs.nodir]
command = "/bin/sleep 300"
directory = "`+missing+`"
autostart = true
startretries = 0
`)
	for name, want := range map[string]string{
		"nobinary": "spawn error: " + missing + ": no such file or directory",
		"noexec":   "spawn error: " + notExec + ": permission denied",
		"nodir":    "spawn error: directory " + missing + " does not exist",
	} {
		waitForState(t, client, name, "FATAL", 10*time.Second)
		info, err := getProcessInfo(client, name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Description != want {
			t.Errorf("%s: description %q, want %q", name, info.Description, want)
		}
	}
}

// Starting a started process is a 409 with the documented message.
func TestLifecycle_StartErrorText(t *testing.T) {
	client, socketPath := startDaemon(t, `
[programs.sleeper]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)
	err := client.Start("sleeper")
	if err == nil || !strings.Contains(err.Error(), "process already started: sleeper") {
		t.Fatalf("start: err %v, want \"process already started: sleeper\"", err)
	}
	if code, body := postAPI(t, socketPath, "/api/v1/processes/sleeper/start"); code != http.StatusConflict {
		t.Fatalf("POST start: %d %s, want 409", code, body)
	}
}

// The stop signal is delivered to the child: TERM by default, stopsignal
// otherwise.
func TestLifecycle_StopSignalDelivered(t *testing.T) {
	for _, sig := range []string{"TERM", "INT", "USR1"} {
		t.Run(sig, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "got")
			ready := filepath.Join(dir, "ready")
			script := writeScript(t, dir, "trap.sh", fmt.Sprintf(
				"trap 'echo %s > %s; exit 0' %s\necho ready > %s\nwhile true; do sleep 0.1; done", sig, marker, sig, ready))
			stopsignal := ""
			if sig != "TERM" {
				stopsignal = "stopsignal = \"" + sig + "\"\n"
			}
			client, _ := startDaemon(t, `
[programs.trap]
command = "`+script+`"
autostart = true
startsecs = 0
stopwaitsecs = 10
`+stopsignal)
			waitForState(t, client, "trap", "RUNNING", 5*time.Second)
			waitForMarker(t, ready, "ready", 5*time.Second)
			if err := client.Stop("trap"); err != nil {
				t.Fatalf("stop: %v", err)
			}
			waitForState(t, client, "trap", "STOPPED", 5*time.Second)
			waitForMarker(t, marker, sig, time.Second)
		})
	}
}

// stopasgroup signals the whole process group, killasgroup SIGKILLs it on
// escalation; without them a grandchild outlives the stop.
func TestLifecycle_StopAsGroup(t *testing.T) {
	for _, tc := range []struct {
		name, trap, settings string
		grandchildDies       bool
	}{
		{name: "default", settings: "", grandchildDies: false},
		{name: "stopasgroup", settings: "stopasgroup = true\nkillasgroup = true\n", grandchildDies: true},
		{name: "killasgroup", trap: "trap '' TERM\n", settings: "killasgroup = true\nstopwaitsecs = 1\n", grandchildDies: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gcFile := filepath.Join(dir, "gc.pid")
			script := writeScript(t, dir, "tree.sh", tc.trap+"sleep 300 &\necho $! > "+gcFile+"\nwait")
			client, _ := startDaemon(t, `
[programs.tree]
command = "`+script+`"
autostart = true
startsecs = 0
`+tc.settings)
			waitForState(t, client, "tree", "RUNNING", 5*time.Second)
			gc := readPIDFile(t, gcFile)
			t.Cleanup(func() { _ = syscall.Kill(gc, syscall.SIGKILL) })

			if err := client.Stop("tree"); err != nil {
				t.Fatalf("stop: %v", err)
			}
			waitForState(t, client, "tree", "STOPPED", 10*time.Second)
			if tc.grandchildDies {
				waitForDeath(t, gc, 3*time.Second)
				return
			}
			time.Sleep(500 * time.Millisecond)
			if !alive(gc) {
				t.Fatalf("grandchild %d died without stopasgroup/killasgroup", gc)
			}
		})
	}
}

// autorestart after RUNNING: true restarts on any exit, unexpected only on
// an exit code not in exitcodes, false never.
func TestLifecycle_AutorestartAfterRunning(t *testing.T) {
	for _, tc := range []struct {
		name, autorestart string
		exit              int
		restarts          bool
	}{
		{"true exit 0", `"true"`, 0, true},
		{"true exit 1", `"true"`, 1, true},
		{"unexpected exit 1", `"unexpected"`, 1, true},
		{"unexpected exit 2", `"unexpected"`, 2, false},
		{"unexpected exit 0", `"unexpected"`, 0, false},
		{"false exit 1", `"false"`, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			runs := filepath.Join(dir, "runs")
			script := writeScript(t, dir, "run.sh", fmt.Sprintf("echo run >> %s\nsleep 1.5\nexit %d", runs, tc.exit))
			client, _ := startDaemon(t, `
[programs.job]
command = "`+script+`"
autostart = true
startsecs = 1
autorestart = `+tc.autorestart+`
exitcodes = [0, 2]
`)
			if tc.restarts {
				waitForLines(t, runs, 2, 10*time.Second)
				return
			}
			waitForState(t, client, "job", "EXITED", 10*time.Second)
			time.Sleep(2 * time.Second)
			info, _ := getProcessInfo(client, "job")
			if got := countLines(runs); got != 1 || info.State != "EXITED" || info.ExitStatus != tc.exit {
				t.Fatalf("runs %d, state %s, exit %d; want 1 run, EXITED, exit %d", got, info.State, info.ExitStatus, tc.exit)
			}
		})
	}
}

// A stopped process is never autorestarted.
func TestLifecycle_NoRespawnAfterStop(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := writeScript(t, dir, "run.sh", "echo run >> "+runs+"\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.job]
command = "`+script+`"
autostart = true
startsecs = 0
autorestart = "true"
`)
	waitForState(t, client, "job", "RUNNING", 5*time.Second)
	// With startsecs = 0 the process is RUNNING before its script has run.
	waitForLines(t, runs, 1, 5*time.Second)
	if err := client.Stop("job"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForState(t, client, "job", "STOPPED", 10*time.Second)
	time.Sleep(2 * time.Second)
	info, _ := getProcessInfo(client, "job")
	if got := countLines(runs); got != 1 || info.State != "STOPPED" {
		t.Fatalf("runs %d, state %s after stop; want 1 run, STOPPED", got, info.State)
	}
}

// Daemon shutdown does not autorestart a child that exits on SIGTERM.
func TestLifecycle_NoRespawnDuringShutdown(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := writeScript(t, dir, "run.sh", "echo run >> "+runs+"\ntrap 'exit 1' TERM\nwhile true; do sleep 0.1; done")

	_, _, done, client, _ := shutdownDaemon(t, 10, `
[programs.worker]
command = "`+script+`"
autostart = true
startsecs = 0
autorestart = "true"
`)
	waitForLines(t, runs, 1, 5*time.Second)
	if err := client.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("daemon did not exit")
	}
	time.Sleep(500 * time.Millisecond)
	if got := countLines(runs); got != 1 {
		t.Fatalf("spawned %d times, want 1: shutdown respawned the child", got)
	}
}

// Invalid lifecycle settings are config errors.
func TestLifecycle_ConfigErrors(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
	}{
		{
			name:   "invalid autorestart",
			config: "[programs.web]\ncommand = \"/bin/sleep 300\"\nautorestart = \"sometimes\"\n",
			want:   `programs.web: autorestart must be true, false, or unexpected, got "sometimes"`,
		},
		{
			name:   "autorestart of another type",
			config: "[programs.web]\ncommand = \"/bin/sleep 300\"\nautorestart = 1\n",
			want:   `autorestart must be true, false, or "unexpected", got 1`,
		},
		{
			name:   "stopasgroup without killasgroup",
			config: "[programs.web]\ncommand = \"/bin/sleep 300\"\nstopasgroup = true\nkillasgroup = false\n",
			want:   "programs.web: killasgroup cannot be false when stopasgroup is true",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr, err := runDaemonExpectFailure(t, tc.config)
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
				t.Fatalf("daemon err = %v, want a non-zero exit; stderr:\n%s", err, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
		})
	}
}

// Children that exit at the same moment are each reaped with their own exit
// status.
func TestLifecycle_SimultaneousExits(t *testing.T) {
	dir := t.TempDir()
	var cfg strings.Builder
	for i := 1; i <= 5; i++ {
		script := writeScript(t, dir, fmt.Sprintf("p%d.sh", i), fmt.Sprintf("sleep 1\nexit %d", 10+i))
		fmt.Fprintf(&cfg, "[programs.p%d]\ncommand = %q\nautostart = true\nstartsecs = 0\nautorestart = \"false\"\n\n", i, script)
	}
	client, _ := startDaemon(t, cfg.String())
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("p%d", i)
		waitForState(t, client, name, "EXITED", 10*time.Second)
		info, err := getProcessInfo(client, name)
		if err != nil {
			t.Fatal(err)
		}
		if info.ExitStatus != 10+i || info.PID != 0 {
			t.Errorf("%s: exit %d pid %d, want exit %d pid 0", name, info.ExitStatus, info.PID, 10+i)
		}
	}
}
