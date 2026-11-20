package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A write that would take the file past maxbytes rotates first (FUNC-019):
// the backup holds only what was there, the new file the write.
func TestCaptureRotatesBeforeWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	cw, err := NewCaptureWriter(CaptureConfig{ProcessName: "app", Logfile: path, MaxBytes: "10", Backups: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()
	_, _ = cw.Write([]byte("12345678"))
	_, _ = cw.Write([]byte("abcd"))
	if b, _ := os.ReadFile(path + ".1"); string(b) != "12345678" {
		t.Errorf("backup %q, want 12345678", b)
	}
	if b, _ := os.ReadFile(path); string(b) != "abcd" {
		t.Errorf("current %q, want abcd", b)
	}
}

// A rotation that fails is not retried, and logged, on every write (#299).
func TestCaptureFailedRotationBacksOff(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root renames in a read-only directory")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	var logs bytes.Buffer
	cw, err := NewCaptureWriter(CaptureConfig{ProcessName: "app", Logfile: path, MaxBytes: "10", Backups: 1,
		Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()
	_, _ = cw.Write([]byte("12345678"))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	for range 50 {
		_, _ = cw.Write([]byte("abcd"))
	}
	if n := strings.Count(logs.String(), "log rotation failed"); n != 1 {
		t.Errorf("%d rotation errors logged for 50 writes, want 1:\n%s", n, logs.String())
	}
	if b, _ := os.ReadFile(path); len(b) != 8+50*4 {
		t.Errorf("current file %d bytes, want every write kept", len(b))
	}
}

// SIGUSR2 (Reopen) gives a writer that lost its file the file back (#299).
func TestCaptureReopenRecoversLostFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	cw, err := NewCaptureWriter(CaptureConfig{ProcessName: "app", Logfile: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()
	cw.mu.Lock()
	_ = cw.file.Close()
	cw.file = nil // as after a failed reopen following rotation
	cw.mu.Unlock()
	if err := cw.Reopen(); err != nil {
		t.Fatal(err)
	}
	_, _ = cw.Write([]byte("back"))
	if b, _ := os.ReadFile(path); string(b) != "back" {
		t.Errorf("file %q, want back", b)
	}
}
