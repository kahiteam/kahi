package logging

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipeToWriterBasic(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.log")

	cw, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "web",
		Stream:      "stdout",
		Logfile:     logFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()

	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		PipeToWriter(pr, cw, nil)
		close(done)
	}()

	_, _ = pw.Write([]byte("hello world\n"))
	_, _ = pw.Write([]byte("second line\n"))
	_ = pw.Close()
	<-done

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello world") {
		t.Fatalf("log file = %q, want 'hello world'", data)
	}
	if !strings.Contains(string(data), "second line") {
		t.Fatalf("log file = %q, want 'second line'", data)
	}
}

func TestPipeToWriterContainerJSON(t *testing.T) {
	cw, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "worker",
		Stream:      "stderr",
		Logfile:     "", // container mode: no file
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()

	var containerBuf bytes.Buffer
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		PipeToWriter(pr, cw, &containerBuf)
		close(done)
	}()

	_, _ = pw.Write([]byte("error message\n"))
	_ = pw.Close()
	<-done

	output := containerBuf.String()
	if !strings.Contains(output, "error message") {
		t.Fatalf("container output = %q, want 'error message'", output)
	}
	if !strings.Contains(output, `"process":"worker"`) {
		t.Fatalf("container output missing process name: %q", output)
	}
	if !strings.Contains(output, `"stream":"stderr"`) {
		t.Fatalf("container output missing stream: %q", output)
	}
}

func TestCleanupStaleLogsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	emptyLog := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(emptyLog, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	err := CleanupStaleLogs(dir, []string{emptyLog})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(emptyLog); !os.IsNotExist(err) {
		t.Fatal("empty log file should have been removed")
	}
}

func TestCleanupStaleLogsNonEmpty(t *testing.T) {
	dir := t.TempDir()
	nonEmpty := filepath.Join(dir, "active.log")
	if err := os.WriteFile(nonEmpty, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	err := CleanupStaleLogs(dir, []string{nonEmpty})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(nonEmpty); err != nil {
		t.Fatal("non-empty log should not have been removed")
	}
}

func TestCleanupStaleLogsNonexistent(t *testing.T) {
	err := CleanupStaleLogs("/tmp", []string{"/nonexistent/file.log"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCleanupStaleLogsEmptyPattern(t *testing.T) {
	err := CleanupStaleLogs("/tmp", []string{""})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReopenAfterDelete(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "reopen.log")

	cw, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "web",
		Stream:      "stdout",
		Logfile:     logFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()

	// Write initial data.
	_, _ = cw.Write([]byte("before\n"))

	// Delete the log file (simulating rotation).
	_ = os.Remove(logFile)

	// Reopen should recreate.
	if err := cw.Reopen(); err != nil {
		t.Fatal(err)
	}

	// Write after reopen.
	_, _ = cw.Write([]byte("after\n"))

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "after") {
		t.Fatalf("data = %q, want 'after'", data)
	}
	// "before" should NOT be in the new file.
	if strings.Contains(string(data), "before") {
		t.Fatal("old data should not be in reopened file")
	}
}

func TestReopenNoFile(t *testing.T) {
	cw, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "web",
		Stream:      "stdout",
		Logfile:     "", // no file
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()

	// Reopen with no file should be a no-op.
	if err := cw.Reopen(); err != nil {
		t.Fatal(err)
	}
}

func TestRotateIfNeededExceedsSize(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "rotate.log")

	// Write data exceeding 100 bytes.
	data := strings.Repeat("x", 200)
	if err := os.WriteFile(logFile, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	err := RotateIfNeeded(logFile, RotationConfig{
		Maxbytes: "100B",
		Backups:  2,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Original file should be rotated to .1.
	if _, err := os.Stat(logFile + ".1"); err != nil {
		t.Fatal("expected .1 backup file after rotation")
	}
}

func TestRotateIfNeededUnderSize(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "small.log")

	if err := os.WriteFile(logFile, []byte("small"), 0644); err != nil {
		t.Fatal(err)
	}

	err := RotateIfNeeded(logFile, RotationConfig{
		Maxbytes: "1MB",
		Backups:  2,
	})
	if err != nil {
		t.Fatal(err)
	}

	// File should still exist (not rotated).
	if _, err := os.Stat(logFile); err != nil {
		t.Fatal("file should still exist")
	}
}

func TestRotateIfNeededUnlimited(t *testing.T) {
	err := RotateIfNeeded("/nonexistent", RotationConfig{
		Maxbytes: "0",
		Backups:  0,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRotateIfNeededMissingFile(t *testing.T) {
	err := RotateIfNeeded("/nonexistent/file.log", RotationConfig{
		Maxbytes: "100B",
		Backups:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSyslogForwarder(t *testing.T) {
	sf, err := NewSyslogForwarder("kahi-test")
	if err != nil {
		t.Skip("syslog not available:", err)
	}
	defer func() { _ = sf.Close() }()

	n, err := sf.Write([]byte("test message"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 12 {
		t.Fatalf("n = %d, want 12", n)
	}
}

func TestRotateIfNeededZeroBackups(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "truncate.log")

	data := strings.Repeat("x", 200)
	if err := os.WriteFile(logFile, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	err := RotateIfNeeded(logFile, RotationConfig{
		Maxbytes: "100B",
		Backups:  0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// File should be truncated, not rotated.
	info, err := os.Stat(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("file size = %d, want 0 (truncated)", info.Size())
	}
}

func TestFormatJSONLineFields(t *testing.T) {
	line := FormatJSONLine("web", "stdout", "hello")
	s := string(line)
	if !strings.Contains(s, `"time":`) {
		t.Fatalf("missing time field: %s", line)
	}
	if !strings.HasSuffix(s, "\n") {
		t.Fatal("expected trailing newline")
	}
}
