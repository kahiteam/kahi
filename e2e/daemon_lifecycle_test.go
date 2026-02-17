//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

func TestDaemon_Health(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.sleeper]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	h, err := client.Health()
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if h != "ok" {
		t.Fatalf("health = %q, want ok", h)
	}
}

func TestDaemon_Version(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.sleeper]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	v, err := client.Version()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	for _, key := range []string{"version", "commit", "date"} {
		if _, ok := v[key]; !ok {
			t.Errorf("version map missing key %q", key)
		}
	}
}

func TestDaemon_PID(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.sleeper]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	pid, err := client.PID("")
	if err != nil {
		t.Fatalf("pid: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(pid))
	if err != nil {
		t.Fatalf("pid not numeric: %q", pid)
	}
	if n <= 1 {
		t.Fatalf("pid = %d, want > 1", n)
	}
}

func TestDaemon_Ready(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.sleeper]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	status, err := client.Ready(nil)
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if !strings.Contains(status, "ready") {
		t.Fatalf("ready status = %q, want 'ready'", status)
	}
}

func TestDaemon_Shutdown(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "kahi.sock")
	configPath := filepath.Join(dir, "kahi.toml")

	config := fmt.Sprintf("[supervisor]\nlog_level = \"debug\"\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n\n[programs.sleeper]\ncommand = \"/bin/sleep 300\"\nautostart = true\nstartsecs = 0\n", socketPath)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := startDaemonCmd(t, kahiBinary, configPath, dir)

	waitForSocket(t, socketPath, 5*time.Second)
	client := ctl.NewUnixClient(socketPath)
	waitForHealth(t, client, 3*time.Second)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	if err := client.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	// Wait for process to exit.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon exited with error: %v", err)
		}
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("daemon did not exit within 15 seconds")
	}
}

func TestDaemon_ShutdownTimeout(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "stubborn.sh", "trap '' TERM\nsleep 300")

	client, _ := startDaemon(t, `
[programs.stubborn]
command = "`+script+`"
autostart = true
startsecs = 0
stopwaitsecs = 2
`)
	waitForState(t, client, "stubborn", "RUNNING", 5*time.Second)

	start := time.Now()
	err := client.Shutdown()
	if err != nil {
		// Shutdown may return error if connection drops during shutdown.
		t.Logf("shutdown returned: %v (expected if connection dropped)", err)
	}
	elapsed := time.Since(start)

	// Shutdown should complete within a reasonable time (stopwaitsecs + buffer).
	if elapsed > 30*time.Second {
		t.Fatalf("shutdown took %v, expected < 30s", elapsed)
	}
}

func TestDaemon_Daemonize(t *testing.T) {
	t.Skip("Go runtime does not reliably support fork (multi-threaded); daemonize needs re-exec implementation")
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "kahi.sock")
	configPath := filepath.Join(dir, "kahi.toml")
	pidFile := filepath.Join(dir, "kahi.pid")

	config := fmt.Sprintf("[supervisor]\nlog_level = \"debug\"\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n\n[programs.sleeper]\ncommand = \"/bin/sleep 300\"\nautostart = true\nstartsecs = 0\n", socketPath)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Start with -d (daemonize) and -p (pid file).
	cmd := startDaemonCmd(t, kahiBinary, configPath, dir, "-d", "-p", pidFile)

	// Parent should exit quickly.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("parent process exited with error: %v", err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("parent did not exit within 10 seconds after daemonize")
	}

	// Daemon should be running in background.
	waitForSocket(t, socketPath, 5*time.Second)
	client := ctl.NewUnixClient(socketPath)
	waitForHealth(t, client, 3*time.Second)

	h, err := client.Health()
	if err != nil {
		t.Fatalf("health after daemonize: %v", err)
	}
	if h != "ok" {
		t.Fatalf("health = %q, want ok", h)
	}

	// Clean up: shutdown the daemonized process.
	t.Cleanup(func() {
		_ = client.Shutdown()
		// Give it time to shut down.
		time.Sleep(2 * time.Second)
	})
}

func TestDaemon_PIDFile(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "kahi.sock")
	configPath := filepath.Join(dir, "kahi.toml")
	pidFile := filepath.Join(dir, "kahi.pid")

	config := fmt.Sprintf("[supervisor]\nlog_level = \"debug\"\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n\n[programs.sleeper]\ncommand = \"/bin/sleep 300\"\nautostart = true\nstartsecs = 0\n", socketPath)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := startDaemonCmd(t, kahiBinary, configPath, dir, "-p", pidFile)
	t.Cleanup(func() {
		c := ctl.NewUnixClient(socketPath)
		_ = c.Shutdown()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		case <-done:
		}
	})

	waitForSocket(t, socketPath, 5*time.Second)
	client := ctl.NewUnixClient(socketPath)
	waitForHealth(t, client, 3*time.Second)

	// PID file should exist and contain the daemon PID.
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		t.Fatalf("pid file content not numeric: %q", pidStr)
	}
	if pid <= 1 {
		t.Fatalf("pid = %d, want > 1", pid)
	}

	// Verify it matches the daemon's reported PID.
	reportedPID, err := client.PID("")
	if err != nil {
		t.Fatalf("get daemon pid: %v", err)
	}
	if strings.TrimSpace(reportedPID) != pidStr {
		t.Fatalf("pid file = %s, daemon reports = %s", pidStr, reportedPID)
	}
}

// startDaemonCmd starts a daemon process and returns the exec.Cmd without
// waiting for readiness. Used by tests that manage the lifecycle directly.
func startDaemonCmd(t *testing.T, binary, configPath, dir string, extraFlags ...string) *exec.Cmd {
	t.Helper()
	args := append([]string{"daemon", "-c", configPath}, extraFlags...)
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	return cmd
}
