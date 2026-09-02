package fcgi

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenTCPInvalidAddress(t *testing.T) {
	sock := NewSocket(ProgramConfig{
		SocketPath: "999.999.999.999:12345",
		Protocol:   ProtocolTCP,
	})

	_, err := sock.Open()
	if err == nil {
		_ = sock.Close()
		t.Fatal("expected error for invalid TCP address")
	}
	if !strings.Contains(err.Error(), "cannot bind") {
		t.Fatalf("error = %q, want 'cannot bind'", err)
	}
}

func TestOpenUnixInvalidPath(t *testing.T) {
	sock := NewSocket(ProgramConfig{
		SocketPath: "/nonexistent/dir/deep/test.sock",
		Protocol:   ProtocolUnix,
	})

	_, err := sock.Open()
	if err == nil {
		_ = sock.Close()
		t.Fatal("expected error for invalid unix path")
	}
	if !strings.Contains(err.Error(), "cannot create") {
		t.Fatalf("error = %q, want 'cannot create'", err)
	}
}

func TestCloseNeverOpened(t *testing.T) {
	sock := NewSocket(ProgramConfig{
		SocketPath: "/tmp/never-opened.sock",
		Protocol:   ProtocolUnix,
	})

	err := sock.Close()
	if err != nil {
		t.Fatalf("expected nil on close of unopened socket, got: %v", err)
	}
}

func TestCloseCalledTwice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "double-close.sock")

	sock := NewSocket(ProgramConfig{
		SocketPath: path,
		Protocol:   ProtocolUnix,
	})

	_, err := sock.Open()
	if err != nil {
		t.Fatal(err)
	}

	if err := sock.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}

	if err := sock.Close(); err != nil {
		t.Fatalf("second close should return nil, got: %v", err)
	}
}

func TestCloseTCPSocket(t *testing.T) {
	sock := NewSocket(ProgramConfig{
		SocketPath: "127.0.0.1:0",
		Protocol:   ProtocolTCP,
	})

	_, err := sock.Open()
	if err != nil {
		t.Fatal(err)
	}

	if err := sock.Close(); err != nil {
		t.Fatalf("close TCP socket failed: %v", err)
	}

	if sock.Addr() != "" {
		t.Fatal("expected empty address after TCP close")
	}
}

func TestOpenTCPIdempotent(t *testing.T) {
	sock := NewSocket(ProgramConfig{
		SocketPath: "127.0.0.1:0",
		Protocol:   ProtocolTCP,
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
		t.Fatal("expected same fd on second TCP open")
	}
}
