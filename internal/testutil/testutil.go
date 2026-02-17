// Package testutil provides shared test helpers for the Kahi test suite.
package testutil

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
)

// TempDir creates a temporary directory for testing and registers cleanup.
func TempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kahi-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// FreeSocket returns a unique Unix socket path in a temporary directory.
// The socket file does not exist yet; it is created by the daemon.
func FreeSocket(t *testing.T) string {
	t.Helper()
	dir := TempDir(t)
	return filepath.Join(dir, "kahi.sock")
}

// FreeTCPPort returns an available TCP port by binding to :0 and releasing.
func FreeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot find free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// MustParseConfig parses a TOML string into a Config struct, failing the
// test on error. Intended for concise test setup.
func MustParseConfig(t *testing.T, toml string) *config.Config {
	t.Helper()
	cfg, warnings, err := config.LoadBytes([]byte(toml), "test.toml")
	if err != nil {
		t.Fatalf("MustParseConfig: %v", err)
	}
	for _, w := range warnings {
		t.Logf("config warning: %s", w)
	}
	return cfg
}

// WaitFor polls a condition function until it returns true or the timeout
// expires. Returns an error if the condition is not met within the timeout.
func WaitFor(t *testing.T, condition func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	interval := 50 * time.Millisecond

	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatal("WaitFor: condition not met within timeout")
}

// WriteFile writes content to a file in the given directory.
func WriteFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("cannot write %s: %v", path, err)
	}
	return path
}

// TestDaemon holds a reference to a running test daemon instance.
type TestDaemon struct {
	SocketPath string
	ConfigPath string
	Dir        string
	Cleanup    func()
}

// StartTestDaemon creates a config file and prepares a test daemon environment.
// The daemon process itself is not started (that requires process management
// which is in Session 1 scope). This helper provides the scaffolding.
func StartTestDaemon(t *testing.T, configTOML string) *TestDaemon {
	t.Helper()
	dir := TempDir(t)
	socketPath := filepath.Join(dir, "kahi.sock")

	fullConfig := fmt.Sprintf(`
[supervisor]
log_level = "debug"
log_format = "text"

[server.unix]
file = %q

%s
`, socketPath, configTOML)

	configPath := WriteFile(t, dir, "kahi.toml", fullConfig)

	td := &TestDaemon{
		SocketPath: socketPath,
		ConfigPath: configPath,
		Dir:        dir,
		Cleanup:    func() { _ = os.RemoveAll(dir) },
	}
	t.Cleanup(td.Cleanup)
	return td
}
