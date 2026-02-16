package logging

import (
	"bytes"
	"encoding/json"
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
