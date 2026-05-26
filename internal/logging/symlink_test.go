package logging

import (
	"os"
	"path/filepath"
	"testing"
)

// SEC-012: log files are opened with O_NOFOLLOW, so a symlink planted at the
// configured log path is rejected rather than followed.

func TestCaptureWriterRejectsSymlinkLogfile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	if err := os.WriteFile(target, nil, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "p",
		Stream:      "stdout",
		Logfile:     link,
	}); err == nil {
		t.Fatal("expected error opening a symlinked logfile (O_NOFOLLOW)")
	}
}

func TestCaptureWriterAcceptsRegularLogfile(t *testing.T) {
	dir := t.TempDir()
	cw, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "p",
		Stream:      "stdout",
		Logfile:     filepath.Join(dir, "real.log"),
	})
	if err != nil {
		t.Fatalf("regular logfile should open: %v", err)
	}
	_ = cw.Close()
}

func TestDaemonLoggerRejectsSymlinkLogfile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "d.log")
	if err := os.WriteFile(target, nil, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "dlink.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, _, err := DaemonLogger("info", "json", link); err == nil {
		t.Fatal("expected error opening a symlinked daemon logfile (O_NOFOLLOW)")
	}
}
