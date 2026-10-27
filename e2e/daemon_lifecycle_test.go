//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	ready := filepath.Join(dir, "ready")
	script := writeScript(t, dir, "stubborn.sh", "trap '' TERM\necho ready > "+ready+"\nsleep 300")

	client, _ := startDaemon(t, `
[programs.stubborn]
command = "`+script+`"
autostart = true
startsecs = 0
stopwaitsecs = 2
`)
	waitForState(t, client, "stubborn", "RUNNING", 5*time.Second)
	waitForMarker(t, ready, "ready", 5*time.Second)

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

// shutdownDaemon starts a daemon with the given shutdown_timeout and programs,
// waits for the one named worker to run, and returns the daemon command,
// worker's PID, a channel that receives the daemon's exit, a client and the
// socket path.
func shutdownDaemon(t *testing.T, shutdownTimeout int, program string) (*exec.Cmd, int, <-chan error, *ctl.Client, string) {
	t.Helper()
	// Short temp dir: macOS caps Unix socket paths at 104 bytes.
	dir, err := os.MkdirTemp("", "k-")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "kahi.sock")
	configPath := filepath.Join(dir, "kahi.toml")

	config := fmt.Sprintf("[supervisor]\nshutdown_timeout = %d\n\n[server.unix]\nfile = %q\n\n%s", shutdownTimeout, socketPath, program)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := startDaemonCmd(t, kahiBinary, configPath, dir)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
	})

	waitForSocket(t, socketPath, 5*time.Second)
	client := ctl.NewUnixClient(socketPath)
	waitForHealth(t, client, 3*time.Second)
	waitForState(t, client, "worker", "RUNNING", 5*time.Second)
	info, err := getProcessInfo(client, "worker")
	if err != nil {
		t.Fatalf("process info: %v", err)
	}
	return cmd, info.PID, done, client, socketPath
}

// A child that exits on SIGTERM must be reaped during shutdown, so the
// daemon exits promptly instead of waiting out stopwaitsecs or
// shutdown_timeout (#90).
func TestDaemon_SIGTERMShutdownIsPrompt(t *testing.T) {
	cmd, _, done, _, _ := shutdownDaemon(t, 30, `
[programs.worker]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
stopwaitsecs = 20
`)

	start := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal daemon: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon exited with error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("daemon did not exit within 15 seconds")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("shutdown took %v; exited children are not being reaped", elapsed)
	}
}

// When shutdown_timeout expires, a child that ignores its stop signal must
// be SIGKILLed rather than left running after the daemon exits.
func TestDaemon_ShutdownTimeoutKillsChildren(t *testing.T) {
	dir := t.TempDir()
	// The ignored disposition survives exec, so the PID kahi tracks is the
	// one that ignores SIGTERM.
	// The ready file is written after the trap: with startsecs = 0 the
	// daemon is signalled as soon as the worker is RUNNING.
	ready := filepath.Join(dir, "ready")
	script := writeScript(t, dir, "stubborn.sh", "trap '' TERM\necho ready > "+ready+"\nexec sleep 300")

	cmd, pid, done, _, _ := shutdownDaemon(t, 2, `
[programs.worker]
command = "`+script+`"
autostart = true
startsecs = 0
stopwaitsecs = 60
`)
	waitForMarker(t, ready, "ready", 5*time.Second)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal daemon: %v", err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("daemon did not exit within 15 seconds")
	}

	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("child %d outlived the daemon (kill 0: %v)", pid, err)
	}
}

// While a SIGTERM shutdown is in progress, /healthz must report it and the
// API must refuse to start processes (#108); before, both only applied to a
// shutdown requested through the API.
func TestDaemon_SignalShutdownIsReported(t *testing.T) {
	dir := t.TempDir()
	// Ignores SIGTERM, so shutdown stays in progress until shutdown_timeout.
	// The ready file is written after the trap: with startsecs = 0 the
	// daemon is signalled as soon as the worker is RUNNING.
	ready := filepath.Join(dir, "ready")
	script := writeScript(t, dir, "stubborn.sh", "trap '' TERM\necho ready > "+ready+"\nexec sleep 300")

	cmd, _, done, client, socketPath := shutdownDaemon(t, 5, `
[programs.worker]
command = "`+script+`"
autostart = true
startsecs = 0
stopwaitsecs = 60

[programs.idle]
command = "/bin/sleep 300"
autostart = false
`)
	waitForMarker(t, ready, "ready", 5*time.Second)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal daemon: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		if s, err := client.Health(); err == nil {
			status = s
			if s == "shutting_down" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status != "shutting_down" {
		t.Fatalf("/healthz status during signal shutdown = %q, want shutting_down", status)
	}
	// The client reads only the body; the HTTP status must be 503 too.
	if code := healthzStatusCode(t, socketPath); code != http.StatusServiceUnavailable {
		t.Fatalf("/healthz during signal shutdown returned %d, want 503", code)
	}

	err := client.Start("idle")
	if err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("start during signal shutdown: err = %v, want a shutting-down refusal", err)
	}

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("daemon did not exit within 15 seconds")
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

// A second daemon with the same config must refuse to start instead of
// taking over the running daemon's control socket (#110).
func TestDaemon_SecondInstanceRefused(t *testing.T) {
	client, socketPath := startDaemon(t, `
[programs.sleeper]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	dir := filepath.Dir(socketPath)

	// Bounded: a second daemon that wrongly starts would otherwise run forever.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	second := exec.CommandContext(ctx, kahiBinary, "daemon", "-c", filepath.Join(dir, "kahi.toml"))
	second.Dir = dir
	out, err := second.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("second daemon started and kept running; output:\n%s", out)
	}
	if err == nil {
		t.Fatalf("second daemon exited 0; output:\n%s", out)
	}
	if !strings.Contains(string(out), "another kahi daemon is running") {
		t.Fatalf("second daemon output does not name the running daemon:\n%s", out)
	}

	if h, err := client.Health(); err != nil || h != "ok" {
		t.Fatalf("first daemon unreachable after second start attempt: %q, %v", h, err)
	}
}

// healthzStatusCode returns the HTTP status of GET /healthz over the Unix
// socket; the ctl client exposes only the body.
func healthzStatusCode(t *testing.T, socketPath string) int {
	t.Helper()
	hc := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}
	resp, err := hc.Get("http://unix/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}
