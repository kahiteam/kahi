package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestUnixControlSocketOwnerOnlyMode covers SEC-022: the served control socket
// carries the exact owner-only permission bits it was bound with, so no group
// or other access is ever exposed on the transport that skips authentication.
func TestUnixControlSocketOwnerOnlyMode(t *testing.T) {
	srv, _, _ := testServer()
	// Use /tmp directly to avoid long macOS temp paths exceeding the Unix
	// socket path length limit (see TestUnixSocketCleanupOnShutdown).
	sockPath := filepath.Join("/tmp", fmt.Sprintf("kahi-owner-%d.sock", os.Getpid()))
	t.Cleanup(func() { os.Remove(sockPath) })

	if err := srv.StartUnix(sockPath, 0o700); err != nil {
		t.Fatalf("StartUnix: %v", err)
	}
	defer func() { _ = srv.Stop(context.Background()) }()

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("socket mode = %#o, want 0700 (owner-only)", perm)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("socket grants group/other access: %#o", info.Mode().Perm())
	}
}

// TestUnixControlSocketBindFailureServesNothing covers the acceptance criterion
// that a failure while binding the socket closes out cleanly and leaves no
// listener serving, so a world-accessible socket is never exposed.
func TestUnixControlSocketBindFailureServesNothing(t *testing.T) {
	srv, _, _ := testServer()
	// Path inside a directory that does not exist: net.Listen fails, so the
	// socket is never bound and no listener is left running.
	sockPath := filepath.Join(t.TempDir(), "missing-dir", "x.sock")

	if err := srv.StartUnix(sockPath, 0o700); err == nil {
		_ = srv.Stop(context.Background())
		t.Fatal("expected StartUnix to fail on an unbindable path")
	}
	if addr := srv.UnixAddr(); addr != "" {
		t.Fatalf("expected no listener after bind failure, got addr %q", addr)
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatalf("expected no socket file after bind failure, stat err = %v", err)
	}
}
