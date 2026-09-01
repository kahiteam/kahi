package fcgi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSocketOpenUnix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sock")

	sock := NewSocket(ProgramConfig{
		SocketPath: path,
		Protocol:   ProtocolUnix,
		SocketMode: 0666,
	})

	fd, err := sock.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sock.Close() }()

	if fd == nil {
		t.Fatal("expected non-nil file descriptor")
	}

	// Verify socket file exists.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatal("expected socket file")
	}

	if sock.Addr() == "" {
		t.Fatal("expected non-empty address")
	}
}

func TestSocketOpenTCP(t *testing.T) {
	sock := NewSocket(ProgramConfig{
		SocketPath: "127.0.0.1:0",
		Protocol:   ProtocolTCP,
	})

	fd, err := sock.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sock.Close() }()

	if fd == nil {
		t.Fatal("expected non-nil file descriptor")
	}
	if sock.Addr() == "" {
		t.Fatal("expected non-empty address")
	}
}

func TestSocketClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sock")

	sock := NewSocket(ProgramConfig{
		SocketPath: path,
		Protocol:   ProtocolUnix,
	})

	_, err := sock.Open()
	if err != nil {
		t.Fatal(err)
	}

	if err := sock.Close(); err != nil {
		t.Fatal(err)
	}

	// Socket file should be cleaned up.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expected socket file to be removed after close")
	}

	if sock.Addr() != "" {
		t.Fatal("expected empty address after close")
	}
}

func TestSocketOpenIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sock")

	sock := NewSocket(ProgramConfig{
		SocketPath: path,
		Protocol:   ProtocolUnix,
	})

	fd1, err := sock.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sock.Close() }()

	fd2, err := sock.Open()
	if err != nil {
		t.Fatal(err)
	}

	if fd1 != fd2 {
		t.Fatal("expected same file descriptor on second open")
	}
}

func TestSocketInvalidProtocol(t *testing.T) {
	sock := NewSocket(ProgramConfig{
		SocketPath: "/tmp/test.sock",
		Protocol:   "invalid",
	})

	_, err := sock.Open()
	if err == nil {
		t.Fatal("expected error for invalid protocol")
	}
}
