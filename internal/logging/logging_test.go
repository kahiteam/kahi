package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := New(LogConfig{Output: &buf})
	logger.Info("test message", "key", "value")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}

	ts, _ := entry["time"].(string)
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("timestamp not RFC3339: %q", ts)
	}

	if level, _ := entry["level"].(string); level != "INFO" {
		t.Errorf("level = %q, want INFO", level)
	}

	if msg, _ := entry["msg"].(string); msg != "test message" {
		t.Errorf("msg = %q, want %q", msg, "test message")
	}

	if v, _ := entry["key"].(string); v != "value" {
		t.Errorf("key = %q, want %q", v, "value")
	}
}

func TestTextFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := New(LogConfig{Format: "text", Output: &buf})
	logger.Info("hello text")

	out := buf.String()
	if !strings.Contains(out, "hello text") {
		t.Errorf("text output missing message: %q", out)
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err == nil {
		t.Error("text format should not produce valid JSON")
	}
}

func TestLevelFiltering(t *testing.T) {
	tests := []struct {
		name      string
		cfgLevel  string
		logLevel  string
		wantEmpty bool
	}{
		{"error cfg filters info", "error", "info", true},
		{"error cfg keeps error", "error", "error", false},
		{"debug cfg keeps debug", "debug", "debug", false},
		{"debug cfg keeps info", "debug", "info", false},
		{"warn cfg filters info", "warn", "info", true},
		{"warn cfg keeps warn", "warn", "warn", false},
		{"info cfg filters debug", "info", "debug", true},
		{"info cfg keeps info", "info", "info", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(LogConfig{Level: tc.cfgLevel, Output: &buf})

			switch tc.logLevel {
			case "debug":
				logger.Debug("test")
			case "info":
				logger.Info("test")
			case "warn":
				logger.Warn("test")
			case "error":
				logger.Error("test")
			}

			got := buf.String()
			if tc.wantEmpty && got != "" {
				t.Fatalf("expected no output, got: %s", got)
			}
			if !tc.wantEmpty && got == "" {
				t.Fatal("expected output, got nothing")
			}
		})
	}
}

func TestChildLoggerWithFields(t *testing.T) {
	var buf bytes.Buffer
	logger := New(LogConfig{Output: &buf})
	child := WithFields(logger, "process", "foo")
	child.Info("child message")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if v, _ := entry["process"].(string); v != "foo" {
		t.Errorf("process = %q, want %q", v, "foo")
	}
}

func TestValidateLevel(t *testing.T) {
	valid := []string{"debug", "info", "warn", "error", "DEBUG", "Info", " warn "}
	for _, lvl := range valid {
		if err := ValidateLevel(lvl); err != nil {
			t.Errorf("ValidateLevel(%q) returned error: %v", lvl, err)
		}
	}

	invalid := []string{"", "trace", "fatal", "nonsense", "WARNING"}
	for _, lvl := range invalid {
		if err := ValidateLevel(lvl); err == nil {
			t.Errorf("ValidateLevel(%q) expected error, got nil", lvl)
		}
	}
}

func TestNewLevelVar(t *testing.T) {
	lv := NewLevelVar("warn")
	if got := lv.Level(); got != slog.LevelWarn {
		t.Errorf("NewLevelVar(\"warn\").Level() = %v, want %v", got, slog.LevelWarn)
	}
}

func TestLevelVarSet(t *testing.T) {
	lv := NewLevelVar("info")
	if got := lv.Level(); got != slog.LevelInfo {
		t.Fatalf("initial level = %v, want %v", got, slog.LevelInfo)
	}

	lv.Set("error")
	if got := lv.Level(); got != slog.LevelError {
		t.Errorf("after Set(\"error\"), Level() = %v, want %v", got, slog.LevelError)
	}

	lv.Set("debug")
	if got := lv.Level(); got != slog.LevelDebug {
		t.Errorf("after Set(\"debug\"), Level() = %v, want %v", got, slog.LevelDebug)
	}
}

func TestLevelVarLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown", slog.LevelInfo}, // default
	}

	for _, tc := range tests {
		lv := NewLevelVar(tc.input)
		if got := lv.Level(); got != tc.want {
			t.Errorf("NewLevelVar(%q).Level() = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestOpenDaemonLogStdout(t *testing.T) {
	dl, err := OpenDaemonLog("info", "json", "")
	if err != nil {
		t.Fatalf("OpenDaemonLog with empty logfile: %v", err)
	}
	if dl.Logger == nil {
		t.Fatal("logger should not be nil")
	}
	// Reopen and Close are no-ops without a file.
	if err := dl.Reopen(); err != nil {
		t.Errorf("Reopen without a file: %v", err)
	}
	if err := dl.Close(); err != nil {
		t.Errorf("Close without a file: %v", err)
	}
}

func TestOpenDaemonLogFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")

	dl, err := OpenDaemonLog("debug", "json", path)
	if err != nil {
		t.Fatalf("OpenDaemonLog with temp file: %v", err)
	}
	defer func() { _ = dl.Close() }()

	dl.Logger.Info("daemon file test", "key", "val")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if !strings.Contains(string(data), "daemon file test") {
		t.Errorf("log file missing expected message, got: %s", string(data))
	}
}

func TestOpenDaemonLogBadPath(t *testing.T) {
	_, err := OpenDaemonLog("info", "json", "/no/such/directory/logfile.log")
	if err == nil {
		t.Fatal("expected error for invalid log path, got nil")
	}
	if !strings.Contains(err.Error(), "cannot open log file") {
		t.Errorf("error message = %q, want it to contain %q", err.Error(), "cannot open log file")
	}
}

// After logrotate renames the file, Reopen makes later writes land in a new
// file at the original path (#129).
func TestReopenableFileReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.log")
	f, err := OpenReopenableFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	_, _ = f.Write([]byte("before\n"))
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := f.Reopen(); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_, _ = f.Write([]byte("after\n"))

	if b, _ := os.ReadFile(path); string(b) != "after\n" {
		t.Fatalf("new file = %q, want only the line written after reopen", b)
	}
	if b, _ := os.ReadFile(path + ".1"); string(b) != "before\n" {
		t.Fatalf("rotated file = %q, want only the line written before", b)
	}
}

// A failed reopen keeps writing to the old file instead of losing output.
func TestReopenableFileReopenFailureKeepsOldFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "kahi.log")
	f, err := OpenReopenableFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	rotated := filepath.Join(t.TempDir(), "kahi.log.1")
	if err := os.Rename(path, rotated); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(dir)
	if err := f.Reopen(); err == nil {
		t.Fatal("reopen into a missing directory succeeded")
	}
	if _, err := f.Write([]byte("still here\n")); err != nil {
		t.Fatalf("write after failed reopen: %v", err)
	}
	if b, _ := os.ReadFile(rotated); string(b) != "still here\n" {
		t.Fatalf("old file = %q, want the write to land there", b)
	}
}
