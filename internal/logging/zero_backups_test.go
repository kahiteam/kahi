package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With backups = 0, a log file swapped for a symlink must not let the
// truncate reach the link's target (#138, SEC-012).
func TestRotateFileZeroBackupsRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(dir, "app.log")
	if err := os.Symlink(victim, logFile); err != nil {
		t.Fatal(err)
	}

	if err := rotateFile(logFile, 0); err == nil {
		t.Fatal("rotateFile truncated through a symlink, want an error")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep me" {
		t.Fatalf("symlink target = %q, want it untouched", b)
	}
}

// A capture writer with backups = 0 empties its own file in place when it
// reaches maxbytes, keeps writing to it, and leaves no backup behind.
func TestCaptureWriterZeroBackupsTruncatesInPlace(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "app.log")
	cw, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "app",
		Stream:      "stdout",
		Logfile:     logFile,
		MaxBytes:    "100B",
		Backups:     0,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cw.Close() })

	if _, err := cw.Write([]byte(strings.Repeat("x", 150) + "\n")); err != nil {
		t.Fatal(err)
	}
	// The next write finds the file over its limit and empties it first.
	if _, err := cw.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "after") || len(b) > 100 {
		t.Fatalf("log = %q (%d bytes), want the file emptied and then written", b, len(b))
	}
	if _, err := os.Stat(logFile + ".1"); !os.IsNotExist(err) {
		t.Fatalf("backup %s.1 exists (err %v), want none with backups = 0", logFile, err)
	}

	// Swap the open file's path for a symlink: the in-place truncate acts on
	// the open handle, so the link's target is never touched.
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(logFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, logFile); err != nil {
		t.Fatal(err)
	}
	if _, err := cw.Write([]byte(strings.Repeat("y", 150) + "\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := cw.Write([]byte("again\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep me" {
		t.Fatalf("symlink target = %q, want it untouched", b)
	}
}
