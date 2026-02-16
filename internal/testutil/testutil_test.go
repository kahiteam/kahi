package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTempDir(t *testing.T) {
	dir := TempDir(t)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("temp dir does not exist: %v", err)
	}
}

func TestFreeSocket(t *testing.T) {
	sock := FreeSocket(t)
	if sock == "" {
		t.Fatal("empty socket path")
	}
	if !strings.HasSuffix(sock, "kahi.sock") {
		t.Errorf("socket path = %q, want suffix kahi.sock", sock)
	}
	// Socket file should not exist yet.
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Error("socket file should not exist yet")
	}
}

func TestFreeTCPPort(t *testing.T) {
	port := FreeTCPPort(t)
	if port <= 0 || port > 65535 {
		t.Fatalf("invalid port: %d", port)
	}
}

func TestMustParseConfig(t *testing.T) {
	toml := `
[programs.web]
command = "/usr/bin/python app.py"
`
	cfg := MustParseConfig(t, toml)
	if cfg == nil {
		t.Fatal("config is nil")
	}
	if _, ok := cfg.Programs["web"]; !ok {
		t.Error("missing programs.web")
	}
}

func TestWaitFor(t *testing.T) {
	counter := 0
	WaitFor(t, func() bool {
		counter++
		return counter >= 3
	}, 5*time.Second)

	if counter < 3 {
		t.Errorf("counter = %d, want >= 3", counter)
	}
}

func TestWriteFile(t *testing.T) {
	dir := TempDir(t)
	path := WriteFile(t, dir, "test.txt", "hello")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want hello", string(data))
	}
}

func TestStartTestDaemon(t *testing.T) {
	td := StartTestDaemon(t, `
[programs.web]
command = "/bin/echo hello"
`)
	if td.SocketPath == "" {
		t.Error("empty socket path")
	}
	if td.ConfigPath == "" {
		t.Error("empty config path")
	}

	// Config file should exist and be valid.
	data, err := os.ReadFile(td.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "kahi.sock") {
		t.Error("config missing socket path")
	}
	if !strings.Contains(content, "programs.web") {
		t.Error("config missing programs.web")
	}

	// Dir should exist.
	if _, err := os.Stat(td.Dir); err != nil {
		t.Fatalf("dir does not exist: %v", err)
	}

	// Temp dir should contain config file.
	entries, _ := os.ReadDir(td.Dir)
	found := false
	for _, e := range entries {
		if e.Name() == filepath.Base(td.ConfigPath) {
			found = true
		}
	}
	if !found {
		t.Error("config file not in daemon dir")
	}
}
