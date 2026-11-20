//go:build unix

package logging

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// SEC-019 (decided 2026-11-20): a log path that is a hard link to another
// file is refused on every open, so a daemon running as root never appends
// to (or truncates) a file a program user linked into its log directory.

// plantHardLink makes logpath a second name of target and returns target's
// original content.
func plantHardLink(t *testing.T, dir string) (target, logpath string) {
	t.Helper()
	target = filepath.Join(dir, "protected")
	if err := os.WriteFile(target, []byte("KEEP\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	logpath = filepath.Join(dir, "app.log")
	if err := os.Link(target, logpath); err != nil {
		t.Fatal(err)
	}
	return target, logpath
}

func assertUntouched(t *testing.T, target string) {
	t.Helper()
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "KEEP\n" {
		t.Fatalf("linked target changed: %q", got)
	}
}

func TestLogOpenRefusesHardLink(t *testing.T) {
	target, logpath := plantHardLink(t, t.TempDir())
	f, err := openLogFile(logpath, 0o644)
	if err == nil {
		_ = f.Close()
		t.Fatal("open of a hard-linked log succeeded")
	}
	if !strings.Contains(err.Error(), "2 hard links; refusing to write a log file linked elsewhere") {
		t.Fatalf("error = %v", err)
	}
	assertUntouched(t, target)
}

func TestLogOpenRefusesFIFOWithoutBlocking(t *testing.T) {
	logpath := filepath.Join(t.TempDir(), "app.log")
	if err := syscall.Mkfifo(logpath, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	// With a reader present a write open of a FIFO succeeds, so the
	// regular-file check is what refuses it.
	rd, err := unix.Open(logpath, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(rd) }()
	done := make(chan error, 1)
	go func() {
		f, err := openLogFile(logpath, 0o644)
		if f != nil {
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("open of a FIFO blocked")
	}
}

func TestLogOpenClearsNonblock(t *testing.T) {
	logpath := filepath.Join(t.TempDir(), "app.log")
	f, err := openLogFile(logpath, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.O_NONBLOCK != 0 {
		t.Fatal("log file descriptor left non-blocking")
	}
	if _, err := f.WriteString("line\n"); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureWriterRefusesHardLinkOnEveryOpen(t *testing.T) {
	dir := t.TempDir()
	target, logpath := plantHardLink(t, dir)

	cw := &CaptureWriter{config: CaptureConfig{Logfile: logpath}}
	if err := cw.EnsureOpen(); err == nil || !strings.Contains(err.Error(), "hard links") {
		t.Fatalf("EnsureOpen error = %v", err)
	}
	if err := cw.Reopen(); err == nil || !strings.Contains(err.Error(), "hard links") {
		t.Fatalf("Reopen error = %v", err)
	}
	if err := rotateFile(logpath, 0); err == nil || !strings.Contains(err.Error(), "hard links") {
		t.Fatalf("zero-backups rotation error = %v", err)
	}
	if _, err := OpenReopenableFile(logpath); err == nil || !strings.Contains(err.Error(), "hard links") {
		t.Fatalf("daemon log open error = %v", err)
	}
	assertUntouched(t, target)
}

func TestReopenableFileReopenRefusesHardLink(t *testing.T) {
	dir := t.TempDir()
	logpath := filepath.Join(dir, "daemon.log")
	r, err := OpenReopenableFile(logpath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	target := filepath.Join(dir, "protected")
	if err := os.WriteFile(target, []byte("KEEP\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(logpath); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, logpath); err != nil {
		t.Fatal(err)
	}
	if err := r.Reopen(); err == nil || !strings.Contains(err.Error(), "hard links") {
		t.Fatalf("Reopen error = %v", err)
	}
	assertUntouched(t, target)
}

func TestProbeLogFileRefusesHardLinkAndFIFO(t *testing.T) {
	_, logpath := plantHardLink(t, t.TempDir())
	if err := probeLogFile(logpath); err == nil || !strings.Contains(err.Error(), "2 hard links") {
		t.Fatalf("probe of a hard link = %v", err)
	}
	fifo := filepath.Join(t.TempDir(), "app.log")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if err := probeLogFile(fifo); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("probe of a FIFO = %v", err)
	}
	regular := filepath.Join(t.TempDir(), "ok.log")
	if err := os.WriteFile(regular, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := probeLogFile(regular); err != nil {
		t.Fatalf("probe of a regular file = %v", err)
	}
}
