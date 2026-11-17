package ctl

import (
	"bytes"
	"testing"
)

func TestIsTerminalBuffer(t *testing.T) {
	var buf bytes.Buffer
	if isTerminal(&buf) {
		t.Fatal("bytes.Buffer should not be detected as terminal")
	}
}

func TestNewUnixClientTransport(t *testing.T) {
	c := NewUnixClient("/tmp/test-kahi.sock")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.baseURL != "http://unix" {
		t.Fatalf("baseURL = %q, want http://unix", c.baseURL)
	}
	if c.httpClient.Transport == nil {
		t.Fatal("expected non-nil transport")
	}
}

func TestPIDConnectionError(t *testing.T) {
	c := NewTCPClient("127.0.0.1:1", "", "")
	_, err := c.PID("web")
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestHealthConnectionError(t *testing.T) {
	c := NewTCPClient("127.0.0.1:1", "", "")
	_, err := c.Health()
	if err == nil {
		t.Fatal("expected connection error")
	}
}
