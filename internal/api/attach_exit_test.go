package api

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/events"
)

// A process that exits between Attach and the event subscription ends the
// session at once: the exit event is not delivered, so the handler checks
// the state after subscribing (#298).
func TestAttachExitBeforeSubscription(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sess := &mockSession{in: make(chan []byte, 4), released: make(chan struct{})}
	pm := &mockProcessManager{processes: []ProcessInfo{{Name: "web", Group: "web", State: "EXITED"}}, attach: sess}
	srv := NewServer(Config{}, pm, &mockGroupManager{}, &mockConfigManager{}, &mockDaemonInfo{}, events.NewBus(logger), logger)
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
	typ, _, err := ReadFrame(br)
	if err != nil || typ != FrameExited {
		t.Fatalf("frame %d %v, want the exit frame", typ, err)
	}
	select {
	case <-sess.released:
	case <-time.After(5 * time.Second):
		t.Fatal("hold not released")
	}
}
