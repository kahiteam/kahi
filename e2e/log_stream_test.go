//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The SSE log stream with byte-offset ids and resume (FUNC-027, ADR-033,
// #155).

type sseFrame struct {
	event, id, data string
}

// logStream opens path (a /log/{stream}/stream URL) over the socket with
// Last-Event-ID lastID ("" sends none) and returns the response and its
// frames; the stream ends with the test.
func logStream(t *testing.T, socket, path, lastID string) (*http.Response, <-chan sseFrame) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+path, nil)
	req.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	out := make(chan sseFrame, 256)
	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		var f sseFrame
		var data []string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				f.data = strings.Join(data, "\n")
				out <- f
				f, data = sseFrame{}, nil
			case strings.HasPrefix(line, "event: "):
				f.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "id: "):
				f.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				data = append(data, strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	return resp, out
}

func nextFrame(t *testing.T, frames <-chan sseFrame, timeout time.Duration) sseFrame {
	t.Helper()
	select {
	case f, ok := <-frames:
		if !ok {
			t.Fatal("log stream closed")
		}
		return f
	case <-time.After(timeout):
		t.Fatalf("no log frame within %v", timeout)
	}
	return sseFrame{}
}

func noFrame(t *testing.T, frames <-chan sseFrame, wait time.Duration) {
	t.Helper()
	select {
	case f := <-frames:
		t.Fatalf("unexpected frame %+v", f)
	case <-time.After(wait):
	}
}

// sendStdin writes data to a process's stdin through the API.
func sendStdin(t *testing.T, socket, name, data string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"data": data})
	var resp map[string]string
	apiDo(t, socket, "POST", "/api/v1/processes/"+name+"/stdin", string(body)).jsonBody(t, "stdin", http.StatusOK, &resp)
}

// splitID splits "{generation}-{offset}".
func splitID(t *testing.T, id string) (string, int64) {
	t.Helper()
	gen, off, ok := strings.Cut(id, "-")
	n, err := strconv.ParseInt(off, 10, 64)
	if !ok || gen == "" || err != nil {
		t.Fatalf("id %q is not {generation}-{offset}", id)
	}
	return gen, n
}

const catProgram = `
[programs.web]
command = "/bin/cat"
startsecs = 0
`

// Ids are byte offsets; a resume replays only what follows; resuming at the
// last id replays nothing; no Last-Event-ID (or an empty one) is live only.
func TestLogStream_IDsAndResume(t *testing.T) {
	t.Parallel()
	client, socket := startDaemon(t, catProgram)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	const path = "/api/v1/processes/web/log/stdout/stream"

	resp, frames := logStream(t, socket, path, "")
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" || resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers %v", resp.Header)
	}
	sendStdin(t, socket, "web", "a\n")
	f := nextFrame(t, frames, 5*time.Second)
	gen, off := splitID(t, f.id)
	if f.event != "" || off != 2 || f.data != "a\n" {
		t.Fatalf("first frame %+v, want an unnamed event with offset 2 and data a", f)
	}
	sendStdin(t, socket, "web", "bc\n")
	if f = nextFrame(t, frames, 5*time.Second); f.id != gen+"-5" || f.data != "bc\n" {
		t.Fatalf("second frame %+v, want id %s-5", f, gen)
	}

	// Resume after "a\n": one replayed event with the end id.
	_, resumed := logStream(t, socket, path, gen+"-2")
	if f = nextFrame(t, resumed, 5*time.Second); f.id != gen+"-5" || f.data != "bc\n" {
		t.Fatalf("replay %+v, want bc ending at %s-5", f, gen)
	}
	// Resume at the last id: nothing replayed, then live output.
	_, atEnd := logStream(t, socket, path, gen+"-5")
	noFrame(t, atEnd, 300*time.Millisecond)
	// Live only, without and with an empty Last-Event-ID.
	_, live := logStream(t, socket, path, "")
	sendStdin(t, socket, "web", "d\n")
	for name, ch := range map[string]<-chan sseFrame{"at end": atEnd, "live": live, "resumed": resumed, "first": frames} {
		if f = nextFrame(t, ch, 5*time.Second); f.id != gen+"-7" || f.data != "d\n" {
			t.Errorf("%s stream: %+v, want d at %s-7", name, f, gen)
		}
	}

	for _, bad := range []string{"abc", gen + "-x", gen + "--1"} {
		r := apiDoHeader(t, socket, path, "Last-Event-ID", bad)
		r.wantError(t, "Last-Event-ID "+bad, http.StatusBadRequest, "invalid resume position: "+bad, "BAD_REQUEST")
	}
	apiDo(t, socket, "GET", "/api/v1/processes/nosuch/log/stdout/stream", "").
		wantError(t, "nosuch", http.StatusNotFound, "no such process: nosuch", "NOT_FOUND")
	apiDo(t, socket, "GET", "/api/v1/processes/web/log/stdin/stream", "").
		wantError(t, "stdin", http.StatusBadRequest, "stream must be stdout or stderr", "BAD_REQUEST")
}

// apiDoHeader is a GET with one request header.
func apiDoHeader(t *testing.T, socket, path, key, value string) apiResponse {
	t.Helper()
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	req, _ := http.NewRequest(http.MethodGet, "http://unix"+path, nil)
	req.Header.Set(key, value)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var b strings.Builder
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		b.WriteString(sc.Text())
	}
	return apiResponse{status: resp.StatusCode, header: resp.Header, body: []byte(b.String())}
}

// Past 64 KB a resume from 0 announces the gap before the held bytes.
func TestLogStream_Gap(t *testing.T) {
	t.Parallel()
	script := writeScript(t, t.TempDir(), "big.sh", "head -c 70000 /dev/zero | tr '\\000' a\nexec sleep 300")
	client, socket := startDaemon(t, `
[programs.big]
command = "`+script+`"
startsecs = 0
`)
	waitForState(t, client, "big", "RUNNING", 5*time.Second)
	const path = "/api/v1/processes/big/log/stdout/stream"
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := apiDo(t, socket, "GET", "/api/v1/processes/big/log/stdout?length=65536", "")
		if len(r.body) == 65536 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("held %d bytes after 5s, want 65536", len(r.body))
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Learn the generation from a resume of another generation (reset).
	_, frames := logStream(t, socket, path, "other-0")
	if f := nextFrame(t, frames, 5*time.Second); f.event != "reset" || f.id != "" {
		t.Fatalf("frame %+v, want a reset without id", f)
	}
	f := nextFrame(t, frames, 5*time.Second)
	gen, off := splitID(t, f.id)
	if off != 70000 || len(f.data) != 65536 {
		t.Fatalf("replay ends at %d with %d bytes, want 70000 and 65536", off, len(f.data))
	}

	_, frames = logStream(t, socket, path, gen+"-0")
	if f = nextFrame(t, frames, 5*time.Second); f.event != "gap" || f.data != `{"bytes": 4464}` || f.id != "" {
		t.Fatalf("frame %+v, want gap of 4464 bytes without id", f)
	}
	if f = nextFrame(t, frames, 5*time.Second); f.id != gen+"-70000" || len(f.data) != 65536 {
		t.Fatalf("replay %q... id %s, want 65536 bytes at %s-70000", f.data[:min(10, len(f.data))], f.id, gen)
	}
}

// A process created again (reload or a new daemon) has a new generation: an
// old id gets a reset, even when the new process wrote more than the old
// offset.
func TestLogStream_ResetAfterRecreate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "talk.sh", "echo \"$1-this-line-is-longer-than-the-old-offset\"\nexec cat")
	prog := func(v string) string {
		return "[programs.web]\ncommand = \"" + script + " " + v + "\"\nstartsecs = 0\n"
	}
	client, socket := startDaemon(t, prog("v1"))
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	const path = "/api/v1/processes/web/log/stdout/stream"
	_, frames := logStream(t, socket, path, "x-0")
	nextFrame(t, frames, 5*time.Second) // reset
	oldGen, _ := splitID(t, nextFrame(t, frames, 5*time.Second).id)
	oldID := oldGen + "-2"

	rewriteConfig(t, socket, prog("v2"))
	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	time.Sleep(300 * time.Millisecond)

	_, frames = logStream(t, socket, path, oldID)
	if f := nextFrame(t, frames, 5*time.Second); f.event != "reset" || f.data != "{}" {
		t.Fatalf("frame %+v, want reset", f)
	}
	f := nextFrame(t, frames, 5*time.Second)
	newGen, _ := splitID(t, f.id)
	if newGen == oldGen || !strings.HasPrefix(f.data, "v2-") {
		t.Fatalf("replay %+v, want the v2 output under a new generation", f)
	}

	// A different daemon: the old id resets there too.
	_, other := startDaemon(t, catProgram)
	time.Sleep(300 * time.Millisecond)
	_, frames = logStream(t, other, path, oldID)
	if f = nextFrame(t, frames, 5*time.Second); f.event != "reset" {
		t.Fatalf("frame %+v on another daemon, want reset", f)
	}
}

// stderr has its own stream; with redirect_stderr it goes to stdout and the
// stderr stream stays empty. Output lines that look like SSE fields stay
// data (SEC-013). strip_ansi applies to live and replayed data alike.
func TestLogStream_StderrRedirectAndFraming(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	errScript := writeScript(t, dir, "err.sh", "while read l; do echo \"$l\" >&2; done")
	client, socket := startDaemon(t, `
[programs.errs]
command = "`+errScript+`"
startsecs = 0

[programs.merged]
command = "`+errScript+`"
startsecs = 0
redirect_stderr = true

[programs.color]
command = "/bin/cat"
startsecs = 0
strip_ansi = true
`)
	for _, p := range []string{"errs", "merged", "color"} {
		waitForState(t, client, p, "RUNNING", 5*time.Second)
	}

	_, errs := logStream(t, socket, "/api/v1/processes/errs/log/stderr/stream", "")
	sendStdin(t, socket, "errs", "oops\n")
	if f := nextFrame(t, errs, 5*time.Second); f.data != "oops\n" {
		t.Fatalf("stderr frame %+v", f)
	}

	_, mOut := logStream(t, socket, "/api/v1/processes/merged/log/stdout/stream", "")
	_, mErr := logStream(t, socket, "/api/v1/processes/merged/log/stderr/stream", "")
	sendStdin(t, socket, "merged", "event: injected\n")
	if f := nextFrame(t, mOut, 5*time.Second); f.event != "" || f.data != "event: injected\n" {
		t.Fatalf("merged stdout frame %+v, want the line as data", f)
	}
	noFrame(t, mErr, 300*time.Millisecond)

	_, color := logStream(t, socket, "/api/v1/processes/color/log/stdout/stream", "")
	sendStdin(t, socket, "color", "\x1b[31mred\x1b[0m\n")
	live := nextFrame(t, color, 5*time.Second)
	gen, _ := splitID(t, live.id)
	_, replay := logStream(t, socket, "/api/v1/processes/color/log/stdout/stream", gen+"-0")
	if again := nextFrame(t, replay, 5*time.Second); live.data != "red\n" || again.data != live.data {
		t.Fatalf("live %q, replay %q, want both stripped to red", live.data, again.data)
	}
}

// The event stream filters by exact type names.
func TestLogStream_EventTypesFilter(t *testing.T) {
	t.Parallel()
	client, socket := startDaemon(t, `
[programs.short]
command = "/bin/sh -c 'sleep 1; exit 0'"
startsecs = 0
autostart = false
autorestart = "false"
`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs := streamEvents(t, ctx, socket, "?types=PROCESS_STATE_RUNNING,PROCESS_STATE_EXITED")
	none := streamEvents(t, ctx, socket, "?types=NO_SUCH_TYPE")
	if err := client.Start("short"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for len(got) < 2 {
		select {
		case m := <-msgs:
			got = append(got, m.event)
		case <-time.After(10 * time.Second):
			t.Fatalf("events %v, want RUNNING and EXITED", got)
		}
	}
	if strings.Join(got, ",") != "PROCESS_STATE_RUNNING,PROCESS_STATE_EXITED" {
		t.Fatalf("events %v", got)
	}
	select {
	case m := <-none:
		t.Fatalf("unknown type matched %+v", m)
	case <-time.After(300 * time.Millisecond):
	}
}
