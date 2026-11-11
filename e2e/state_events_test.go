//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Process state events on /api/v1/events/stream (FUNC-050, ADR-032, #154):
// every event carries from_state, EXITED carries expected, exitstatus and
// exitsignal, and STARTING is published before the spawn.

type stateEvent struct {
	typ  string
	data map[string]string
}

// stateEvents opens the event stream and returns a function that waits for
// the next state event of process name, failing the test after timeout.
func stateEvents(t *testing.T, socket, name string) func(timeout time.Duration) stateEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	msgs := streamEvents(t, ctx, socket, "")
	return func(timeout time.Duration) stateEvent {
		t.Helper()
		deadline := time.After(timeout)
		for {
			select {
			case m, ok := <-msgs:
				if !ok {
					t.Fatal("event stream closed")
				}
				if !strings.HasPrefix(m.event, "PROCESS_STATE_") {
					continue
				}
				var raw map[string]any
				if err := json.Unmarshal([]byte(m.data), &raw); err != nil {
					t.Fatalf("event %s data %q: %v", m.event, m.data, err)
				}
				data := map[string]string{}
				for k, v := range raw {
					s, ok := v.(string)
					if !ok {
						t.Errorf("event %s: %s = %v (%T), want a JSON string", m.event, k, v, v)
					}
					data[k] = s
				}
				if data["name"] != name {
					continue
				}
				return stateEvent{typ: m.event, data: data}
			case <-deadline:
				t.Fatalf("no state event for %s within %v", name, timeout)
			}
		}
	}
}

func wantState(t *testing.T, e stateEvent, state, from string) {
	t.Helper()
	if e.typ != "PROCESS_STATE_"+state || e.data["state"] != state || e.data["from_state"] != from {
		t.Fatalf("event %s %v, want %s from %s", e.typ, e.data, state, from)
	}
}

func TestStateEvents_StartRunExit(t *testing.T) {
	client, socket := startDaemon(t, `
[programs.web]
command = "/bin/sleep 300"
autostart = false
startsecs = 1
autorestart = "false"
`)
	next := stateEvents(t, socket, "web")
	if err := client.Start("web"); err != nil {
		t.Fatal(err)
	}
	starting := next(5 * time.Second)
	wantState(t, starting, "STARTING", "STOPPED")
	if starting.data["pid"] != "0" || starting.data["group"] != "web" {
		t.Errorf("STARTING %v, want pid 0 (before the spawn) and group web", starting.data)
	}
	running := next(5 * time.Second)
	wantState(t, running, "RUNNING", "STARTING")
	info, _ := getProcessInfo(client, "web")
	if running.data["pid"] != strconv.Itoa(info.PID) || info.PID == 0 {
		t.Errorf("RUNNING pid %q, process pid %d", running.data["pid"], info.PID)
	}

	// Killed by a signal: unexpected, with the <signal.h> name.
	if err := syscall.Kill(info.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	exited := next(5 * time.Second)
	wantState(t, exited, "EXITED", "RUNNING")
	if exited.data["exitsignal"] != "SIGKILL" || exited.data["exitstatus"] != "-1" || exited.data["expected"] != "false" {
		t.Errorf("EXITED %v, want exitsignal SIGKILL, exitstatus -1, expected false", exited.data)
	}
}

func TestStateEvents_ExitStatus(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "exit.sh", "sleep 1.5\nexit \"$1\"")
	client, socket := startDaemon(t, `
[programs.three]
command = "`+script+` 3"
autostart = false
startsecs = 1
autorestart = "false"
exitcodes = [0]

[programs.zero]
command = "`+script+` 0"
autostart = false
startsecs = 1
autorestart = "false"
exitcodes = [0]
`)
	for _, tc := range []struct{ name, status, expected string }{{"three", "3", "false"}, {"zero", "0", "true"}} {
		next := stateEvents(t, socket, tc.name)
		if err := client.Start(tc.name); err != nil {
			t.Fatal(err)
		}
		wantState(t, next(5*time.Second), "STARTING", "STOPPED")
		wantState(t, next(5*time.Second), "RUNNING", "STARTING")
		exited := next(5 * time.Second)
		wantState(t, exited, "EXITED", "RUNNING")
		if exited.data["exitstatus"] != tc.status || exited.data["expected"] != tc.expected {
			t.Errorf("%s EXITED %v, want exitstatus %s and expected %s", tc.name, exited.data, tc.status, tc.expected)
		}
		if _, ok := exited.data["exitsignal"]; ok {
			t.Errorf("%s EXITED %v carries exitsignal without a signal death", tc.name, exited.data)
		}
	}
}

// STARTING, BACKOFF, STARTING, FATAL: a retry, then giving up in one step,
// reported from STARTING; then the stop path.
func TestStateEvents_BackoffFatalAndStop(t *testing.T) {
	client, socket := startDaemon(t, `
[programs.crash]
command = "/bin/sh -c 'exit 1'"
autostart = false
startsecs = 1
startretries = 1

[programs.never]
command = "/bin/sh -c 'exit 1'"
autostart = false
startsecs = 1
startretries = 0

[programs.web]
command = "/bin/sleep 300"
autostart = false
startsecs = 0
`)
	next := stateEvents(t, socket, "crash")
	_ = client.Start("crash")
	wantState(t, next(5*time.Second), "STARTING", "STOPPED")
	wantState(t, next(5*time.Second), "BACKOFF", "STARTING")
	wantState(t, next(5*time.Second), "STARTING", "BACKOFF")
	wantState(t, next(5*time.Second), "FATAL", "STARTING")

	nextNever := stateEvents(t, socket, "never")
	_ = client.Start("never")
	wantState(t, nextNever(5*time.Second), "STARTING", "STOPPED")
	wantState(t, nextNever(5*time.Second), "FATAL", "STARTING")

	nextWeb := stateEvents(t, socket, "web")
	if err := client.Start("web"); err != nil {
		t.Fatal(err)
	}
	wantState(t, nextWeb(5*time.Second), "STARTING", "STOPPED")
	wantState(t, nextWeb(5*time.Second), "RUNNING", "STARTING")
	if err := client.Stop("web"); err != nil {
		t.Fatal(err)
	}
	wantState(t, nextWeb(5*time.Second), "STOPPING", "RUNNING")
	wantState(t, nextWeb(10*time.Second), "STOPPED", "STOPPING")
}

// SUPERVISOR_STATE_STOPPING arrives once when shutdown begins, naming the
// state it left; supervisor events are not periodic and a reload sends none
// (FUNC-052, #154). SUPERVISOR_STATE_RUNNING is published before any client
// can connect; TestRunPublishesEvents (supervisor) proves it.
func TestStateEvents_SupervisorStopping(t *testing.T) {
	client, socket := startDaemon(t, `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs := streamEvents(t, ctx, socket, "?types=SUPERVISOR_STATE_RUNNING,SUPERVISOR_STATE_STOPPING")

	// A reload and 6 s of running produce no supervisor event.
	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-msgs:
		t.Fatalf("supervisor event while running: %s %s", m.event, m.data)
	case <-time.After(6 * time.Second):
	}

	if err := client.Shutdown(); err != nil {
		t.Fatal(err)
	}
	var stopping []sseMsg
	timeout := time.After(15 * time.Second)
	for done := false; !done; {
		select {
		case m, ok := <-msgs:
			if !ok {
				done = true
				break
			}
			stopping = append(stopping, m)
		case <-timeout:
			t.Fatal("event stream still open 15s after shutdown")
		}
	}
	if len(stopping) != 1 || stopping[0].event != "SUPERVISOR_STATE_STOPPING" ||
		stopping[0].data != `{"from_state":"RUNNING","state":"STOPPING"}` {
		t.Fatalf("events after shutdown %v, want one SUPERVISOR_STATE_STOPPING from RUNNING", stopping)
	}
}
