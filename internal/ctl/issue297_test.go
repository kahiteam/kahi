package ctl

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func shortReconnect(t *testing.T) {
	t.Helper()
	n, d := tailReconnects, tailReconnectDelay
	tailReconnectDelay = 10 * time.Millisecond
	t.Cleanup(func() { tailReconnects, tailReconnectDelay = n, d })
}

// tail -f reconnects with the last event id when the daemon closes the
// stream, and a named event (reset) prints nothing (#297).
func TestTailFollowReconnectsWithLastEventID(t *testing.T) {
	shortReconnect(t)
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Last-Event-ID"))
		n := len(seen)
		mu.Unlock()
		switch n {
		case 1:
			_, _ = fmt.Fprint(w, "id: g-3\ndata: abc\n\n")
		case 2:
			_, _ = fmt.Fprint(w, "event: reset\ndata: {}\n\nid: g-6\ndata: def\n\n")
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"error":"no such process: web"}`)
		}
	}))
	defer srv.Close()
	c := NewTCPClient(strings.TrimPrefix(srv.URL, "http://"), "", "")
	var out bytes.Buffer
	err := c.TailFollow(context.Background(), "web", "stdout", &out)
	if err == nil || err.Error() != "no such process: web" {
		t.Errorf("err %v", err)
	}
	if out.String() != "abcdef" {
		t.Errorf("output %q, want abcdef", out.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 || seen[0] != "" || seen[1] != "g-3" || seen[2] != "g-6" {
		t.Errorf("Last-Event-ID per request %q", seen)
	}
}

// A daemon that cannot be reached again ends tail -f with an error after
// tailReconnects tries, instead of exit 0 with no message.
func TestTailFollowStreamLost(t *testing.T) {
	shortReconnect(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "id: g-1\ndata: x\n\n")
	}))
	c := NewTCPClient(strings.TrimPrefix(srv.URL, "http://"), "", "")
	var once sync.Once
	go func() {
		time.Sleep(50 * time.Millisecond)
		once.Do(srv.Close)
	}()
	defer once.Do(srv.Close)
	err := c.TailFollow(context.Background(), "web", "", &bytes.Buffer{})
	if err == nil || !strings.HasPrefix(err.Error(), "log stream lost: ") {
		t.Errorf("err %v", err)
	}
}

// ctl pid reports the API's error, not "no such process", for a 401.
func TestPIDReportsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":"authentication required","code":"UNAUTHORIZED"}`)
	}))
	defer srv.Close()
	_, err := NewTCPClient(strings.TrimPrefix(srv.URL, "http://"), "", "").PID("web")
	if err == nil || err.Error() != "authentication required" {
		t.Errorf("err %v", err)
	}
}

// A signal to the client (a cancelled ctx) ends attach while the
// handshake waits for an answer that never comes (FUNC-034).
func TestAttachHandshakeCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }() // accept, never answer
		}
	}()
	c := NewTCPClient(ln.Addr().String(), "", "")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	end, err := c.Attach(ctx, "web", strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, AttachOptions{})
	if err != nil || end != AttachInterrupted {
		t.Errorf("end %v, err %v; want AttachInterrupted", end, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}
