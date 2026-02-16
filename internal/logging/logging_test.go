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

func TestDaemonLoggerStdout(t *testing.T) {
	logger, cleanup, err := DaemonLogger("info", "json", "")
	if err != nil {
		t.Fatalf("DaemonLogger with empty logfile: %v", err)
	}
	if cleanup != nil {
		t.Error("cleanup should be nil when no logfile is set")
	}
	if logger == nil {
		t.Fatal("logger should not be nil")
	}
}

func TestDaemonLoggerFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	logger, cleanup, err := DaemonLogger("debug", "json", path)
	if err != nil {
		t.Fatalf("DaemonLogger with temp file: %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup should not be nil when logfile is set")
	}
	defer cleanup()

	if logger == nil {
		t.Fatal("logger should not be nil")
	}

	logger.Info("daemon file test", "key", "val")

	// Ensure data was written to the file.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if !strings.Contains(string(data), "daemon file test") {
		t.Errorf("log file missing expected message, got: %s", string(data))
	}
}

func TestDaemonLoggerBadPath(t *testing.T) {
	_, _, err := DaemonLogger("info", "json", "/no/such/directory/logfile.log")
	if err == nil {
		t.Fatal("expected error for invalid log path, got nil")
	}
	if !strings.Contains(err.Error(), "cannot open log file") {
		t.Errorf("error message = %q, want it to contain %q", err.Error(), "cannot open log file")
	}
}
