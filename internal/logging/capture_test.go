package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureWriterToFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	cw, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "test",
		Stream:      "stdout",
		Logfile:     logPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()

	if _, err := cw.Write([]byte("hello world\n")); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world\n" {
		t.Fatalf("log content = %q, want 'hello world\\n'", string(data))
	}
}

func TestCaptureWriterStripAnsi(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	cw, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "test",
		Stream:      "stdout",
		Logfile:     logPath,
		StripAnsi:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()

	if _, err := cw.Write([]byte("\033[31mERROR\033[0m: something failed\n")); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\033[") {
		t.Fatalf("ANSI codes not stripped: %q", string(data))
	}
	if !strings.Contains(string(data), "ERROR: something failed") {
		t.Fatalf("content not preserved: %q", string(data))
	}
}

func TestCaptureWriterRingBuffer(t *testing.T) {
	cw, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "test",
		Stream:      "stdout",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()

	if _, err := cw.Write([]byte("line 1\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := cw.Write([]byte("line 2\n")); err != nil {
		t.Fatal(err)
	}

	tail := cw.ReadTail(100)
	if !strings.Contains(string(tail), "line 1") {
		t.Fatalf("ring buffer missing 'line 1': %q", string(tail))
	}
	if !strings.Contains(string(tail), "line 2") {
		t.Fatalf("ring buffer missing 'line 2': %q", string(tail))
	}
}

func TestCaptureWriterHandler(t *testing.T) {
	cw, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "test",
		Stream:      "stdout",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()

	var received []byte
	cw.AddHandler(func(name string, data []byte) {
		received = append(received, data...)
	})

	if _, err := cw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if string(received) != "hello" {
		t.Fatalf("handler received %q, want 'hello'", string(received))
	}
}

func TestCaptureWriterReopen(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	cw, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "test",
		Stream:      "stdout",
		Logfile:     logPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cw.Close() }()

	if _, err := cw.Write([]byte("before\n")); err != nil {
		t.Fatal(err)
	}

	// Simulate external rotation: rename the file.
	if err := os.Rename(logPath, logPath+".1"); err != nil {
		t.Fatal(err)
	}

	// Reopen.
	if err := cw.Reopen(); err != nil {
		t.Fatal(err)
	}

	if _, err := cw.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "after\n" {
		t.Fatalf("new file content = %q, want 'after\\n'", string(data))
	}
}

func TestCaptureWriterNonWritablePath(t *testing.T) {
	_, err := NewCaptureWriter(CaptureConfig{
		ProcessName: "test",
		Stream:      "stdout",
		Logfile:     "/nonexistent/dir/test.log",
	})
	if err == nil {
		t.Fatal("expected error for non-writable log path")
	}
}

func TestFormatJSONLine(t *testing.T) {
	line := FormatJSONLine("web", "stdout", "server started")
	s := string(line)
	if !strings.Contains(s, "\"process\":\"web\"") {
		t.Fatalf("missing process field: %s", s)
	}
	if !strings.Contains(s, "\"stream\":\"stdout\"") {
		t.Fatalf("missing stream field: %s", s)
	}
	if !strings.Contains(s, "\"log\":\"server started\"") {
		t.Fatalf("missing log field: %s", s)
	}
}
