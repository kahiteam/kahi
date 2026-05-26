package fcgi

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// shortSocketPath returns a socket path under a short temp dir. The Unix socket
// path is limited (~104 bytes on darwin), so t.TempDir() (which embeds the long
// test name) can overflow it; a short MkdirTemp prefix keeps the path in bounds.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fcgi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// SEC-011: a FastCGI Unix socket must never be left at the umask-dependent
// default. Without socket_mode it must default to 0700; an explicit mode must
// be honored; and a chmod failure must close the listener (fail closed).

func TestUnixSocketDefaultsToOwnerOnly(t *testing.T) {
	path := shortSocketPath(t)

	sock := NewSocket(ProgramConfig{
		SocketPath: path,
		Protocol:   ProtocolUnix,
		// SocketMode intentionally unset (0).
	})
	if _, err := sock.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sock.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Fatalf("socket mode = %04o, want 0700 (owner-only default)", perm)
	}
}

func TestUnixSocketExplicitModeHonored(t *testing.T) {
	path := shortSocketPath(t)

	sock := NewSocket(ProgramConfig{
		SocketPath: path,
		Protocol:   ProtocolUnix,
		SocketMode: 0750,
	})
	if _, err := sock.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sock.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0750 {
		t.Fatalf("socket mode = %04o, want 0750 (explicit)", perm)
	}
}

func TestUnixSocketChmodFailureClosesListener(t *testing.T) {
	orig := chmodSocket
	chmodSocket = func(string, os.FileMode) error { return fmt.Errorf("boom") }
	defer func() { chmodSocket = orig }()

	path := shortSocketPath(t)

	sock := NewSocket(ProgramConfig{
		SocketPath: path,
		Protocol:   ProtocolUnix,
	})
	if _, err := sock.Open(); err == nil {
		t.Fatal("Open must fail when chmod fails (fail closed)")
	}
	if sock.Addr() != "" {
		t.Fatal("listener must be closed after a chmod failure")
	}
}
