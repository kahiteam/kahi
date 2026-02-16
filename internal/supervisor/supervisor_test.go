package supervisor

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewSignalQueue(t *testing.T) {
	sq := NewSignalQueue(testLogger())
	defer sq.Stop()

	if sq.C == nil {
		t.Fatal("expected non-nil signal channel")
	}
}

func TestSignalQueueBufferSize(t *testing.T) {
	sq := NewSignalQueue(testLogger())
	defer sq.Stop()

	// The channel should have a buffer of 16.
	if cap(sq.ch) != 16 {
		t.Fatalf("expected buffer size 16, got %d", cap(sq.ch))
	}
}

func TestSignalQueueStop(t *testing.T) {
	sq := NewSignalQueue(testLogger())
	sq.Stop()
	// After stop, signal.Notify is deregistered. No panic means pass.
}

func TestSignalQueueChannelIdentity(t *testing.T) {
	sq := NewSignalQueue(testLogger())
	defer sq.Stop()

	// The exported C field should be the same underlying channel as ch.
	// Verify by checking they share the same capacity.
	if cap(sq.C) != cap(sq.ch) {
		t.Fatalf("C and ch capacity mismatch: %d vs %d", cap(sq.C), cap(sq.ch))
	}
}

func TestSignalQueueLoggerStored(t *testing.T) {
	logger := testLogger()
	sq := NewSignalQueue(logger)
	defer sq.Stop()

	if sq.logger != logger {
		t.Fatal("expected logger to be stored on SignalQueue")
	}
}

func TestRootWarningNotRoot(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// Override getuid to return non-root.
	original := getuid
	getuid = func() int { return 1000 }
	defer func() { getuid = original }()

	RootWarning(logger, false)

	if strings.Contains(buf.String(), "running as root") {
		t.Fatal("should not warn when not root")
	}
}

func TestRootWarningRootWithUserConfigured(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// Override getuid to simulate root.
	original := getuid
	getuid = func() int { return 0 }
	defer func() { getuid = original }()

	RootWarning(logger, true)

	if strings.Contains(buf.String(), "running as root") {
		t.Fatal("should not warn when user is configured, even as root")
	}
}

func TestRootWarningRootWithoutUserConfigured(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// Override getuid to simulate root.
	original := getuid
	getuid = func() int { return 0 }
	defer func() { getuid = original }()

	RootWarning(logger, false)

	if !strings.Contains(buf.String(), "running as root") {
		t.Fatal("expected root warning when running as root without user config")
	}
}

func TestGetuidDefaultsToOsGetuid(t *testing.T) {
	// Verify the default getuid matches os.Getuid.
	got := getuid()
	want := os.Getuid()
	if got != want {
		t.Fatalf("getuid() = %d, want %d", got, want)
	}
}
