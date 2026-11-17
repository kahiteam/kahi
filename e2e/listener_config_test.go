//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// FUNC-055 config errors refuse to start (plan step 7a).
func TestEventListener_ConfigErrors(t *testing.T) {
	t.Parallel()
	base := "[eventlisteners.l]\ncommand = \"/bin/cat\"\nevents = [\"PROCESS_STATE_EXITED\"]\n"
	for body, want := range map[string]string{
		base + "redirect_stderr = true\n":                  "eventlisteners.l.redirect_stderr: not allowed, stdout carries the protocol",
		base + "buffer_size = 0\n":                         "eventlisteners.l.buffer_size must be >= 1",
		base + "result_timeout = 0\n":                      "eventlisteners.l.result_timeout must be >= 1",
		base + "\n[programs.l]\ncommand = \"/bin/true\"\n": "eventlisteners.l: name already used by programs.l",
	} {
		expectRefused(t, body, want)
	}
}

// An unknown event type and stdout output keys only warn; the daemon runs
// (decided 2026-11-16: a listener typo must not keep the programs from
// starting).
func TestEventListener_ConfigWarnings(t *testing.T) {
	t.Parallel()
	client, _, out := startDaemonOut(t, `[eventlisteners.l]
command = "/bin/cat"
events = ["BOGUS", "PROCESS_STATE_EXITED"]
stdout_logfile = "/tmp/never-written.log"

[programs.web]
command = "/bin/sleep 300"
startsecs = 0
stopwaitsecs = 2
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	o := readOut(out)
	for _, want := range []string{
		"eventlisteners.l.events: unknown event type: BOGUS (valid: ",
		"eventlisteners.l.stdout_logfile has no effect: stdout carries the protocol",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("output lacks %q:\n%s", want, o)
		}
	}
	if strings.Contains(o, "no event listener runs") {
		t.Errorf("stale not-run warning:\n%s", o)
	}
}

// kahi migrate maps [eventlistener:x] and expands supertypes (ADR-042).
func TestMigrate_EventListener(t *testing.T) {
	t.Parallel()
	out, stderr, code := migrate(t, "[eventlistener:x]\ncommand=/bin/cat\nevents=PROCESS_STATE,PROCESS_COMMUNICATION\nbuffer_size=20\n")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	wantLines(t, "stdout", out,
		"[eventlisteners.x]\n",
		`events = ["PROCESS_STATE_STARTING", "PROCESS_STATE_RUNNING", "PROCESS_STATE_BACKOFF", "PROCESS_STATE_STOPPING", "PROCESS_STATE_EXITED", "PROCESS_STATE_STOPPED", "PROCESS_STATE_FATAL"]`,
		"buffer_size = 20\n")
	wantLines(t, "stderr", stderr, "eventlisteners.x: event type PROCESS_COMMUNICATION has no kahi equivalent; dropped")
}
