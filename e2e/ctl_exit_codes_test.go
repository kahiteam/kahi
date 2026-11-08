//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kahi ctl start|stop|restart run against the binary: every target is
// attempted, any failure exits 1 after "{n} of {m} failed", all goes through
// the bulk API, and usage errors exit 2 (FUNC-031, ADR-035, #213).

const ctlPrograms = `
[programs.web]
command = "/bin/sleep 300"
autostart = false
startsecs = 0

[programs.api]
command = "/bin/sleep 300"
autostart = false
startsecs = 0

[programs.worker]
command = "/bin/sleep 300"
autostart = false
startsecs = 0
`

// runCtl runs `kahi ctl -s socket args...`.
func runCtl(t *testing.T, socket string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runKahi(t, t.TempDir(), append([]string{"ctl", "-s", socket}, args...)...)
}

func wantRun(t *testing.T, what string, stdout, stderr string, code, wantCode int, wantOut, wantErr string) {
	t.Helper()
	if code != wantCode || stdout != wantOut || stderr != wantErr {
		t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d, stdout %q, stderr %q",
			what, code, stdout, stderr, wantCode, wantOut, wantErr)
	}
}

func TestCtlExit_StartStop(t *testing.T) {
	client, socket := startDaemon(t, ctlPrograms)

	out, errOut, code := runCtl(t, socket, "start", "web")
	wantRun(t, "start web", out, errOut, code, 0, "web: started\n", "")
	waitForState(t, client, "web", "RUNNING", 5*time.Second)

	out, errOut, code = runCtl(t, socket, "stop", "web")
	wantRun(t, "stop web", out, errOut, code, 0, "web: stopped\n", "")
	waitForState(t, client, "web", "STOPPED", 5*time.Second)

	out, errOut, code = runCtl(t, socket, "start", "web", "api", "worker")
	wantRun(t, "start three", out, errOut, code, 0, "web: started\napi: started\nworker: started\n", "")
	for _, name := range []string{"web", "api", "worker"} {
		waitForState(t, client, name, "RUNNING", 5*time.Second)
	}
}

// One failing target does not stop the others, and the command exits 1.
func TestCtlExit_PartialFailure(t *testing.T) {
	client, socket := startDaemon(t, ctlPrograms)

	out, errOut, code := runCtl(t, socket, "start", "nonexistent", "web")
	wantRun(t, "start nonexistent web", out, errOut, code, 1,
		"web: started\n", "nonexistent: no such process: nonexistent\n1 of 2 failed\n")
	waitForState(t, client, "web", "RUNNING", 5*time.Second)

	_, errOut, code = runCtl(t, socket, "start", "nope:*")
	if code != 1 || !strings.Contains(errOut, "no such group: nope") {
		t.Errorf("start nope:*: exit %d, stderr %q; want 1 and 'no such group: nope'", code, errOut)
	}
}

func TestCtlExit_Groups(t *testing.T) {
	client, socket := startDaemon(t, `
[programs.web]
command = "/bin/sleep 300"
autostart = false
startsecs = 0
numprocs = 2
`)
	out, errOut, code := runCtl(t, socket, "start", "web:*")
	wantRun(t, "start web:*", out, errOut, code, 0, "web:*: started\n", "")
	waitForState(t, client, "web_0", "RUNNING", 5*time.Second)
	waitForState(t, client, "web_1", "RUNNING", 5*time.Second)

	out, errOut, code = runCtl(t, socket, "stop", "web:*")
	wantRun(t, "stop web:*", out, errOut, code, 0, "web:*: stopped\n", "")
	waitForState(t, client, "web_0", "STOPPED", 5*time.Second)
	waitForState(t, client, "web_1", "STOPPED", 5*time.Second)
}

// all goes through the ordered bulk API with one line per process.
func TestCtlExit_All(t *testing.T) {
	client, socket := startDaemon(t, `
[programs.a]
command = "/bin/sleep 300"
startsecs = 0
priority = 1

[programs.b]
command = "/bin/sleep 300"
startsecs = 0
priority = 2

[programs.bad]
command = "/nonexistent/kahi-e2e-binary"
autostart = false
startsecs = 0
startretries = 0
priority = 3
`)
	waitForState(t, client, "a", "RUNNING", 5*time.Second)
	waitForState(t, client, "b", "RUNNING", 5*time.Second)
	a, _ := getProcessInfo(client, "a")
	b, _ := getProcessInfo(client, "b")

	// a and b restart; bad cannot spawn.
	out, errOut, code := runCtl(t, socket, "restart", "all")
	if code != 1 || !strings.HasPrefix(out, "a: ok\nb: ok\nbad: error: ") || errOut != "1 of 3 failed\n" {
		t.Errorf("restart all: exit %d, stdout %q, stderr %q; want a/b ok, bad error, '1 of 3 failed', exit 1", code, out, errOut)
	}
	waitForState(t, client, "a", "RUNNING", 5*time.Second)
	waitForState(t, client, "b", "RUNNING", 5*time.Second)
	if na, _ := getProcessInfo(client, "a"); na.PID == a.PID {
		t.Errorf("a kept pid %d after restart all", a.PID)
	}
	if nb, _ := getProcessInfo(client, "b"); nb.PID == b.PID {
		t.Errorf("b kept pid %d after restart all", b.PID)
	}

	// Running processes are skipped.
	out, errOut, code = runCtl(t, socket, "start", "all")
	if code != 1 || !strings.HasPrefix(out, "a: skipped\nb: skipped\nbad: error: ") || errOut != "1 of 3 failed\n" {
		t.Errorf("start all: exit %d, stdout %q, stderr %q; want skipped lines, bad error, '1 of 3 failed', exit 1", code, out, errOut)
	}

	out, errOut, code = runCtl(t, socket, "stop", "all")
	if code != 0 || !strings.Contains(out, "a: ok\n") || !strings.Contains(out, "b: ok\n") || errOut != "" {
		t.Errorf("stop all: exit %d, stdout %q, stderr %q; want ok lines and exit 0", code, out, errOut)
	}
	waitForState(t, client, "a", "STOPPED", 5*time.Second)
}

// all cannot be mixed with other names; a program named all is all:*.
func TestCtlExit_AllWithNames(t *testing.T) {
	client, socket := startDaemon(t, `
[programs.all]
command = "/bin/sleep 300"
autostart = false
startsecs = 0

[programs.web]
command = "/bin/sleep 300"
startsecs = 0
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)

	out, errOut, code := runCtl(t, socket, "stop", "all", "web")
	wantRun(t, "stop all web", out, errOut, code, 2, "", "all cannot be combined with other names\n")
	if info, _ := getProcessInfo(client, "web"); info.State != "RUNNING" {
		t.Errorf("web %s after a rejected stop, want RUNNING", info.State)
	}

	out, errOut, code = runCtl(t, socket, "start", "all:*")
	wantRun(t, "start all:*", out, errOut, code, 0, "all:*: started\n", "")
	waitForState(t, client, "all", "RUNNING", 5*time.Second)
}

func TestCtlExit_SignalErrors(t *testing.T) {
	client, socket := startDaemon(t, `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)

	out, errOut, code := runCtl(t, socket, "signal", "INVALID", "web")
	wantRun(t, "signal INVALID", out, errOut, code, 1, "", "process web: invalid signal \"INVALID\"\n")

	if _, _, code := runCtl(t, socket, "stop", "web"); code != 0 {
		t.Fatalf("stop web: exit %d", code)
	}
	out, errOut, code = runCtl(t, socket, "signal", "HUP", "web")
	wantRun(t, "signal stopped web", out, errOut, code, 1, "", "process not running: web\n")
}

func TestCtlExit_NoDaemon(t *testing.T) {
	socket := filepath.Join(shortTempDir(t), "none.sock")
	_, errOut, code := runCtl(t, socket, "status")
	if code != 1 || !strings.Contains(errOut, "connection failed") {
		t.Errorf("status without a daemon: exit %d, stderr %q; want 1 and 'connection failed'", code, errOut)
	}
}

// Usage errors exit 2 for every command; kahi ctl alone prints help.
func TestCtlExit_UsageErrors(t *testing.T) {
	dir := t.TempDir()
	out, errOut, code := runKahi(t, dir, "ctl")
	if code != 0 || !strings.Contains(out, "Send commands to a running Kahi daemon") || errOut != "" {
		t.Errorf("kahi ctl: exit %d, stdout %q, stderr %q; want help and exit 0", code, out, errOut)
	}
	for _, args := range [][]string{
		{"ctl", "start"},
		{"ctl", "status", "--bogus"},
		{"daemon", "--bogus"},
		{"migrate"},
		{"bogus"},
		{"ctl", "foo"},
	} {
		if _, errOut, code := runKahi(t, dir, args...); code != 2 || errOut == "" {
			t.Errorf("kahi %v: exit %d, stderr %q; want exit 2 with a message", args, code, errOut)
		}
	}
	_, errOut, _ = runKahi(t, dir, "ctl", "foo")
	if errOut != "unknown command \"foo\" for \"kahi ctl\"\n" {
		t.Errorf("kahi ctl foo: stderr %q", errOut)
	}
}
