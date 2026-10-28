//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TICK events on the SSE stream (FUNC-054, #147).

type sseMsg struct {
	event, data string
	at          time.Time
}

// streamEvents reads /api/v1/events/stream?query over the socket into a
// channel until ctx ends.
func streamEvents(t *testing.T, ctx context.Context, socket, query string) <-chan sseMsg {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/api/v1/events/stream"+query, nil)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("event stream: %v", err)
	}
	out := make(chan sseMsg, 64)
	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()
		sc := bufio.NewScanner(resp.Body)
		var ev string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				out <- sseMsg{event: ev, data: strings.TrimPrefix(line, "data: "), at: time.Now()}
			}
		}
	}()
	return out
}

// Asked for by name, TICK_5 arrives every 5 seconds with its time as
// "when"; the default stream carries no ticks; once shutdown begins, no tick
// arrives while processes are still being stopped.
func TestTickEvents_StreamAndShutdown(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	// Ignores TERM, so shutdown takes stopwaitsecs (8s) and the stream stays
	// open long enough to see whether ticks continue.
	script := writeScript(t, dir, "stubborn.sh", "trap '' TERM\necho ready > "+ready+"\nexec sleep 300")
	_, _, done, client, socket := shutdownDaemon(t, 15, fmt.Sprintf(`
[programs.worker]
command = %q
autostart = true
startsecs = 0
stopwaitsecs = 8
`, script))
	waitForMarker(t, ready, "ready", 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ticks := streamEvents(t, ctx, socket, "?types=TICK_5,SUPERVISOR_STATE_STOPPING")
	all := streamEvents(t, ctx, socket, "")

	var tick sseMsg
	select {
	case tick = <-ticks:
	case <-time.After(7 * time.Second):
		t.Fatal("no TICK_5 within 7s")
	}
	var data map[string]string
	if tick.event != "TICK_5" || json.Unmarshal([]byte(tick.data), &data) != nil {
		t.Fatalf("first event %q %q, want TICK_5 with JSON data", tick.event, tick.data)
	}
	when, err := strconv.ParseInt(data["when"], 10, 64)
	if err != nil || time.Since(time.Unix(when, 0)).Abs() > 5*time.Second {
		t.Fatalf("TICK_5 when = %q, want the current Unix time", data["when"])
	}

	if err := client.Shutdown(); err != nil {
		t.Fatal(err)
	}
	var stopping time.Time
	for stopping.IsZero() {
		select {
		case m, ok := <-ticks:
			if !ok {
				t.Fatal("stream ended before SUPERVISOR_STATE_STOPPING")
			}
			if m.event == "SUPERVISOR_STATE_STOPPING" {
				stopping = m.at
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no SUPERVISOR_STATE_STOPPING")
		}
	}
	// The worker holds shutdown open for 8s; a ticker still running would
	// send TICK_5 in that time.
	for m := range ticks {
		if strings.HasPrefix(m.event, "TICK_") {
			t.Fatalf("%s %.1fs after shutdown began", m.event, m.at.Sub(stopping).Seconds())
		}
	}
	if since := time.Since(stopping); since < 6*time.Second {
		t.Fatalf("stream ended %.1fs after shutdown began; too soon to show that ticks stopped", since.Seconds())
	}
	<-done

	for m := range all {
		if strings.HasPrefix(m.event, "TICK_") {
			t.Fatalf("default stream carried %s", m.event)
		}
	}
}
