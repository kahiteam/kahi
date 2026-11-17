package api

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/kahiteam/kahi/internal/events"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAttachFrames(t *testing.T) {
	var b bytes.Buffer
	if err := WriteFrame(&b, FrameStdout, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	typ, data, err := ReadFrame(&b)
	if err != nil || typ != FrameStdout || string(data) != "hello" {
		t.Fatalf("%d %q %v", typ, data, err)
	}
	big := []byte{FrameStdin, 0xff, 0xff, 0xff, 0xff}
	if _, _, err := ReadFrame(bytes.NewReader(big)); err == nil {
		t.Error("oversized frame accepted")
	}
}

// Errors are JSON and come before any upgrade (FUNC-034).
func TestAttachEndpointErrors(t *testing.T) {
	srv, _, _ := testServer()
	for _, c := range []struct {
		name, upgrade string
		status        int
		msg           string
	}{
		{"web", "", 400, "attach requires Upgrade: kahi-attach"},
		{"nosuch", "kahi-attach", 404, "no such process"},
		{"web", "kahi-attach", 400, "process not running: web"},
	} {
		req := httptest.NewRequest("POST", "/api/v1/processes/"+c.name+"/attach", nil)
		if c.upgrade != "" {
			req.Header.Set("Upgrade", c.upgrade)
			req.Header.Set("Connection", "keep-alive, Upgrade")
		}
		w := httptest.NewRecorder()
		srv.handler.ServeHTTP(w, req)
		if w.Code != c.status || !strings.Contains(w.Body.String(), c.msg) {
			t.Errorf("%s/%q: %d %s", c.name, c.upgrade, w.Code, w.Body)
		}
	}
	for msg, want := range map[string]int{"process web: already attached": http.StatusConflict, "process web: attached": http.StatusConflict} {
		if got := classifyError(errString(msg)); got != want {
			t.Errorf("%q -> %d", msg, got)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// A whole session over the socket: 101, input to the process, output and
// the exit frame back, the hold released at the end (ADR-044).
func TestAttachSession(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sess := &mockSession{in: make(chan []byte, 4), released: make(chan struct{})}
	pm := &mockProcessManager{processes: []ProcessInfo{{Name: "web", Group: "web", State: "RUNNING"}}, attach: sess}
	bus := events.NewBus(logger)
	srv := NewServer(Config{}, pm, &mockGroupManager{}, &mockConfigManager{}, &mockDaemonInfo{}, bus, logger)
	dir, err := os.MkdirTemp("/tmp", "ka-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sock := filepath.Join(dir, "s")
	if err := srv.StartUnix(sock, 0o700); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Stop(context.Background()) }()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "POST /api/v1/processes/web/attach HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: kahi-attach\r\nContent-Length: 0\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("response %v %v", resp, err)
	}
	if err := WriteFrame(conn, FrameStdin, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-sess.in:
		if string(got) != "hi" {
			t.Errorf("input %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("input not delivered")
	}
	for pm.logSubs() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	pm.feedLog("hello", "1")
	typ, data, err := ReadFrame(br)
	if err != nil || (typ != FrameStdout && typ != FrameStderr) || string(data) != "hello" {
		t.Fatalf("frame %d %q %v", typ, data, err)
	}
	bus.Publish(events.Event{Type: events.ProcessStateExited, Data: map[string]string{"name": "web"}})
	for {
		typ, _, err := ReadFrame(br)
		if err != nil {
			t.Fatalf("no exit frame: %v", err)
		}
		if typ == FrameExited {
			break
		}
	}
	select {
	case <-sess.released:
	case <-time.After(5 * time.Second):
		t.Fatal("hold not released")
	}
}
