//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Process log events are opt-in per program and the log stream does not
// depend on them (FUNC-051, ADR-040, #160).

// logEvent waits for an event of type typ for process name on msgs and
// returns its data field.
func logEvent(t *testing.T, msgs <-chan sseMsg, typ, name string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				t.Fatalf("event stream closed waiting for %s of %s", typ, name)
			}
			if m.event != typ {
				continue
			}
			var d map[string]string
			if err := json.Unmarshal([]byte(m.data), &d); err != nil || d["name"] != name {
				continue
			}
			return d["data"]
		case <-deadline:
			t.Fatalf("no %s for %s within %v", typ, name, timeout)
		}
	}
}

func TestLogEvents_OptInAndStripped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	on := writeScript(t, dir, "on.sh", "read l\necho x\necho err >&2\nexec sleep 300")
	ansi := writeScript(t, dir, "ansi.sh", "read l\nprintf '\\033[1mbold\\033[0m\\n'\nexec sleep 300")
	client, socket := startDaemon(t, `
[programs.on]
command = "`+on+`"
startsecs = 0
stdout_events_enabled = true
stderr_events_enabled = true

[programs.ansi]
command = "`+ansi+`"
startsecs = 0
strip_ansi = true
stdout_events_enabled = true
`)
	waitForState(t, client, "on", "RUNNING", 5*time.Second)
	waitForState(t, client, "ansi", "RUNNING", 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs := streamEvents(t, ctx, socket, "?types=PROCESS_LOG_STDOUT,PROCESS_LOG_STDERR")

	sendStdin(t, socket, "on", "go\n")
	// stdout and stderr are separate pipes: their events come in either order.
	got := map[string]string{}
	deadline := time.After(5 * time.Second)
	for len(got) < 2 {
		select {
		case m := <-msgs:
			var d map[string]string
			if json.Unmarshal([]byte(m.data), &d) == nil && d["name"] == "on" {
				got[m.event] = d["data"]
			}
		case <-deadline:
			t.Fatalf("log events of on within 5s: %v, want PROCESS_LOG_STDOUT and PROCESS_LOG_STDERR", got)
		}
	}
	if got["PROCESS_LOG_STDOUT"] != "x\n" || got["PROCESS_LOG_STDERR"] != "err\n" {
		t.Errorf("events %v, want stdout %q and stderr %q", got, "x\n", "err\n")
	}

	sendStdin(t, socket, "ansi", "go\n")
	if got := logEvent(t, msgs, "PROCESS_LOG_STDOUT", "ansi", 5*time.Second); got != "bold\n" {
		t.Errorf("stripped event data %q, want %q", got, "bold\n")
	}
	var buf bytes.Buffer
	if err := client.Tail("ansi", "stdout", 4096, &buf); err != nil || buf.String() != "bold\n" {
		t.Errorf("log read %q (%v), want the same bytes as the event", buf.String(), err)
	}
}

func TestLogEvents_DefaultOffStreamStillWorks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	off := writeScript(t, dir, "off.sh", "read l\necho x")
	ticker := writeScript(t, dir, "ticker.sh", "while true; do echo tick; sleep 0.2; done")
	client, socket := startDaemon(t, `
[programs.off]
command = "`+off+`"
startsecs = 0
autorestart = false

[programs.ticker]
command = "`+ticker+`"
startsecs = 0
`)
	waitForState(t, client, "off", "RUNNING", 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs := streamEvents(t, ctx, socket, "?types=PROCESS_LOG_STDOUT,PROCESS_STATE_EXITED")
	sendStdin(t, socket, "off", "go\n")
	// The EXITED event is the sentinel: no log event may come before it.
	deadline := time.After(5 * time.Second)
wait:
	for {
		select {
		case m := <-msgs:
			switch m.event {
			case "PROCESS_LOG_STDOUT":
				t.Fatalf("log event without stdout_events_enabled: %s", m.data)
			case "PROCESS_STATE_EXITED":
				if strings.Contains(m.data, `"name":"off"`) {
					break wait
				}
			}
		case <-deadline:
			t.Fatal("no PROCESS_STATE_EXITED for off within 5s")
		}
	}
	var buf bytes.Buffer
	if err := client.Tail("off", "stdout", 4096, &buf); err != nil || !strings.Contains(buf.String(), "x") {
		t.Errorf("ctl tail of off = %q (%v), want 'x'", buf.String(), err)
	}

	// The log stream is fed by the capture writer, not the bus (ADR-040).
	_, frames := logStream(t, socket, "/api/v1/processes/ticker/log/stdout/stream", "")
	if f := nextFrame(t, frames, 5*time.Second); !strings.Contains(f.data, "tick") {
		t.Errorf("log stream frame %q, want 'tick'", f.data)
	}
}

func TestLogEvents_RedirectStderr(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := writeScript(t, dir, "r.sh", "read l\necho err >&2\nexec sleep 300")
	client, socket, out := startDaemonOut(t, `
[programs.r]
command = "`+r+`"
startsecs = 0
redirect_stderr = true
stdout_events_enabled = true
stderr_events_enabled = true
`)
	waitForState(t, client, "r", "RUNNING", 5*time.Second)
	b, _ := os.ReadFile(out.Name())
	if !strings.Contains(string(b), "programs.r.stderr_events_enabled has no effect with redirect_stderr") {
		t.Errorf("no redirect warning in daemon output:\n%s", b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs := streamEvents(t, ctx, socket, "?types=PROCESS_LOG_STDOUT,PROCESS_LOG_STDERR")
	sendStdin(t, socket, "r", "go\n")
	if got := logEvent(t, msgs, "PROCESS_LOG_STDOUT", "r", 5*time.Second); got != "err\n" {
		t.Errorf("redirected stderr as PROCESS_LOG_STDOUT %q, want %q", got, "err\n")
	}
}

// A client that never reads the events stream cannot hold up a program's
// output: its queue drops the oldest events and the drops are logged.
func TestLogEvents_StalledClientNeverBlocks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logfile := filepath.Join(dir, "big.log")
	big := writeScript(t, dir, "big.sh", `read l
awk 'BEGIN { s = sprintf("%1023s", ""); gsub(/ /, "a", s); for (i = 0; i < 51200; i++) print s; print "done" }'
exec sleep 300`)
	client, socket, out := startDaemonOut(t, `
[programs.big]
command = "`+big+`"
startsecs = 0
stdout_logfile = "`+logfile+`"
stdout_logfile_maxbytes = "0"
stdout_events_enabled = true
`)
	waitForState(t, client, "big", "RUNNING", 5*time.Second)

	// Open the stream and never read its body.
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/api/v1/events/stream?types=PROCESS_LOG_STDOUT", nil)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("event stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	sendStdin(t, socket, "big", "go\n")
	deadline := time.Now().Add(30 * time.Second)
	for {
		b, _ := os.ReadFile(logfile)
		if bytes.HasSuffix(b, []byte("done\n")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("'done' did not reach the log file within 30s (%d bytes)", len(b))
		}
		time.Sleep(100 * time.Millisecond)
	}
	deadline = time.Now().Add(15 * time.Second)
	for {
		b, _ := os.ReadFile(out.Name())
		if strings.Contains(string(b), "events stream queue full, dropped") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no 'events stream queue full, dropped' in daemon output within 15s of 'done'")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A log event names the process's group, so a listener can address the
// process the way supervisord does (processname, groupname; FUNC-055).
func TestLogEvents_CarryGroup(t *testing.T) {
	t.Parallel()
	script := writeScript(t, t.TempDir(), "g.sh", "read l\necho hi\nexec sleep 300")
	client, socket := startDaemon(t, `
[programs.web]
command = "`+script+`"
startsecs = 0
stdout_events_enabled = true

[groups.site]
programs = ["web"]
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs := streamEvents(t, ctx, socket, "?types=PROCESS_LOG_STDOUT")
	sendStdin(t, socket, "web", "go\n")
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-msgs:
			var d map[string]string
			if m.event != "PROCESS_LOG_STDOUT" || json.Unmarshal([]byte(m.data), &d) != nil || d["name"] != "web" {
				continue
			}
			if d["group"] != "site" || d["data"] != "hi\n" {
				t.Errorf("log event data = %v, want group site and data hi", d)
			}
			return
		case <-deadline:
			t.Fatal("no PROCESS_LOG_STDOUT for web")
		}
	}
}
