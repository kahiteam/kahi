//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// kahi ctl fixes from the v0.3.0 review.

// Process names are escaped in API paths: a name with # or % is started,
// stopped and tailed like any other (it used to 404 or fail before sending).
func TestCtl_SpecialCharacterNames(t *testing.T) {
	t.Parallel()
	client, socket, _ := startDaemonOut(t, "[programs.\"a#b%c\"]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nautostart = false\n")
	name := "a#b%c"
	for _, args := range [][]string{{"start", name}, {"pid", name}, {"signal", "CONT", name}, {"tail", name}, {"stop", name}} {
		if out, stderr, code := ctlRun(t, socket, args...); code != 0 {
			t.Errorf("ctl %v: exit %d, %q %q", args, code, out, stderr)
		}
	}
	waitForState(t, client, name, "STOPPED", 5*time.Second)
}

// A command that takes no arguments refuses them with exit 2: kahi ctl
// reload web used to reload everything (FUNC-031).
func TestCtl_ExtraArgumentsAreUsageErrors(t *testing.T) {
	t.Parallel()
	_, socket, _ := startDaemonOut(t, "[programs.web]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\n")
	for _, args := range [][]string{{"reload", "web"}, {"shutdown", "now"}, {"health", "x"}, {"version", "x"}} {
		if _, _, code := ctlRun(t, socket, args...); code != 2 {
			t.Errorf("ctl %v: exit %d, want 2", args, code)
		}
	}
	for _, args := range [][]string{{"version", "x"}, {"init", "x", "--stdout"}} {
		if _, _, code := runKahi(t, t.TempDir(), args...); code != 2 {
			t.Errorf("kahi %v: exit %d, want 2", args, code)
		}
	}
	if _, _, code := ctlRun(t, socket, "health"); code != 0 {
		t.Error("health without arguments must still work")
	}
}

// status and signal take name:* and all, as completion offers them
// (FUNC-037): status shows those processes, signal sends to each.
func TestCtl_StatusAndSignalGroupTargets(t *testing.T) {
	t.Parallel()
	client, socket, _ := startDaemonOut(t, "[programs.web]\ncommand = \"/bin/sleep 300\"\nnumprocs = 2\nprocess_name = \"web_%(process_num)d\"\nstartsecs = 0\n\n"+
		"[programs.api]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\n\n[groups.svc]\nprograms = [\"api\"]\n")
	for _, n := range []string{"web_0", "web_1", "api"} {
		waitForState(t, client, n, "RUNNING", 5*time.Second)
	}
	lines := func(out string) int { return strings.Count(strings.TrimSpace(out), "\n") } // minus the header
	for target, want := range map[string]int{"web:*": 2, "all": 3, "svc:*": 1, "api:*": 1} {
		out, _, code := ctlRun(t, socket, "status", target)
		if code != 0 || lines(out) != want {
			t.Errorf("status %s: exit %d, %d rows, want %d:\n%s", target, code, lines(out), want, out)
		}
	}
	out, stderr, code := ctlRun(t, socket, "signal", "CONT", "web:*")
	if code != 0 || !strings.Contains(out, "web_0: signaled SIGCONT\n") || !strings.Contains(out, "web_1: signaled SIGCONT\n") {
		t.Errorf("signal web:*: exit %d, %q %q", code, out, stderr)
	}
	if out, _, code := ctlRun(t, socket, "signal", "CONT", "all"); code != 0 || strings.Count(out, "signaled SIGCONT") != 3 {
		t.Errorf("signal all: exit %d, %q", code, out)
	}
	if _, stderr, code := ctlRun(t, socket, "signal", "CONT", "nosuch:*"); code != 1 || !strings.Contains(stderr, "no such group: nosuch") {
		t.Errorf("signal nosuch:*: exit %d, %q", code, stderr)
	}
	if err := client.Stop("api"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "api", "STOPPED", 10*time.Second)
	if _, stderr, code := ctlRun(t, socket, "signal", "CONT", "all"); code != 1 || !strings.Contains(stderr, "1 of 3 failed") {
		t.Errorf("signal all with api stopped: exit %d, %q", code, stderr)
	}
}
