package api

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
	t.Cleanup(func() { _ = os.Remove(sockPath) })

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
// TestUnixControlSocketModeCheckFailureClosesListener covers the fail-closed
// path: when the bound socket is not as configured, the listener is closed
// and nothing is served, so a socket with wider-than-intended permissions
// never stays in service. The control socket skips authentication, so this
// matters more here than anywhere.
func TestUnixControlSocketModeCheckFailureClosesListener(t *testing.T) {
	orig := checkSocketMode
	checkSocketMode = func(string, os.FileMode) error { return fmt.Errorf("boom") }
	t.Cleanup(func() { checkSocketMode = orig })

	srv, _, _ := testServer()
	sockPath := filepath.Join("/tmp", fmt.Sprintf("kahi-modefail-%d.sock", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(sockPath) })

	err := srv.StartUnix(sockPath, 0o700)
	if err == nil {
		_ = srv.Stop(context.Background())
		t.Fatal("StartUnix must fail when the mode check fails (fail closed)")
	}
	if !strings.Contains(err.Error(), "cannot set socket permissions") {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr := srv.UnixAddr(); addr != "" {
		t.Fatalf("listener must be closed after a mode check failure, got addr %q", addr)
	}
	if conn, err := net.Dial("unix", sockPath); err == nil {
		_ = conn.Close()
		t.Fatal("socket still accepts connections after a mode check failure")
	}
}

// The configured mode is applied at bind, with no chmod by path, so a
// symlink at the socket path is never followed to change another file's mode
// (decided 2026-11-20).
func TestVerifySocketModeDoesNotFollow(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "kvm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if err := os.Chmod(sock, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifySocketMode(sock, 0o700); err != nil {
		t.Fatalf("0700 socket: %v", err)
	}
	if err := verifySocketMode(sock, 0o600); err == nil || !strings.Contains(err.Error(), "want 0600") {
		t.Fatalf("mode mismatch: %v", err)
	}
	link := filepath.Join(dir, "l.sock")
	if err := os.Symlink(sock, link); err != nil {
		t.Fatal(err)
	}
	if err := verifySocketMode(link, 0o700); err == nil || !strings.Contains(err.Error(), "is not the bound socket") {
		t.Fatalf("symlink to a socket: %v", err)
	}
	if err := removeStaleSocket(link); err == nil || !strings.Contains(err.Error(), "is not a socket") {
		t.Fatalf("removeStaleSocket through a symlink: %v", err)
	}
	if _, err := ProbeSocket(link); err == nil || !strings.Contains(err.Error(), "is not a socket") {
		t.Fatalf("ProbeSocket through a symlink: %v", err)
	}
}

// The bind applies the configured mode exactly, also under an inherited
// umask that is stricter (it used to be corrected by the chmod).
func TestUnixControlSocketModeUnderStrictUmask(t *testing.T) {
	srv, _, _ := testServer()
	sockPath := filepath.Join("/tmp", fmt.Sprintf("kahi-umask-%d.sock", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(sockPath) })
	old := syscall.Umask(0o277)
	err := srv.StartUnix(sockPath, 0o600)
	syscall.Umask(old)
	if err != nil {
		t.Fatalf("StartUnix: %v", err)
	}
	defer func() { _ = srv.Stop(context.Background()) }()
	fi, err := os.Lstat(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode %#o, want 0600", got)
	}
}

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
