//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// kahiBinary is the path to the built kahi binary, set by TestMain.
var kahiBinary string

// binFalse and binTrue are resolved at init to handle macOS (/usr/bin/) vs Linux (/bin/).
var binFalse string
var binTrue string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "kahi-e2e-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	kahiBinary = filepath.Join(tmpDir, "kahi")
	cmd := exec.Command("go", "build", "-race", "-o", kahiBinary, "github.com/kahiteam/kahi/cmd/kahi")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build kahi binary: %v\n", err)
		os.Exit(1)
	}

	// Resolve portable paths for false/true (macOS: /usr/bin, Linux: /bin).
	binFalse, err = exec.LookPath("false")
	if err != nil {
		fmt.Fprintf(os.Stderr, "required binary 'false' not found: %v\n", err)
		os.Exit(1)
	}
	binTrue, err = exec.LookPath("true")
	if err != nil {
		fmt.Fprintf(os.Stderr, "required binary 'true' not found: %v\n", err)
		os.Exit(1)
	}

	// Suite-wide 10-minute timeout fallback.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	go func() {
		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Fprintln(os.Stderr, "E2E suite timeout exceeded (10 minutes)")
			os.Exit(2)
		}
	}()

	os.Exit(m.Run())
}

// processInfo mirrors the API response for a process.
type processInfo struct {
	Name       string `json:"name"`
	Group      string `json:"group"`
	State      string `json:"state"`
	StateCode  int    `json:"statecode"`
	PID        int    `json:"pid"`
	Uptime     int64  `json:"uptime"`
	ExitStatus int    `json:"exitstatus"`
}

// startDaemon writes configTOML to a temp directory, starts the kahi daemon,
// polls for readiness, and returns a ctl.Client plus a cleanup function.
// The socketPath is injected into the config automatically.
func startDaemon(t *testing.T, configTOML string) (*ctl.Client, string) {
	t.Helper()

	// Use a short temp dir to stay under macOS 104-char Unix socket path limit.
	dir, err := os.MkdirTemp("", "k-")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "kahi.sock")
	configPath := filepath.Join(dir, "kahi.toml")

	fullConfig := fmt.Sprintf("[supervisor]\nlog_level = \"debug\"\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n\n%s", socketPath, configTOML)
	if err := os.WriteFile(configPath, []byte(fullConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, kahiBinary, "daemon", "-c", configPath)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start daemon: %v", err)
	}

	// Cleanup: shutdown then kill.
	t.Cleanup(func() {
		// Try graceful shutdown via client.
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
		cancel()
	})

	// Stage 1: Wait for socket file (5s, 100ms interval).
	waitForSocket(t, socketPath, 5*time.Second)

	client := ctl.NewUnixClient(socketPath)

	// Stage 2: Wait for health endpoint (3s, 50ms interval).
	waitForHealth(t, client, 3*time.Second)

	return client, socketPath
}

// waitForSocket polls for the existence of a Unix socket file.
func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			// Verify it's connectable.
			conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
			if err == nil {
				conn.Close()
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for socket %s", path)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// waitForHealth polls the health endpoint until it returns "ok".
func waitForHealth(t *testing.T, client *ctl.Client, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if h, err := client.Health(); err == nil && h == "ok" {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for health endpoint")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// waitForState polls process state until it matches the expected value.
func waitForState(t *testing.T, client *ctl.Client, name, state string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	var lastState string
	for {
		info, err := getProcessInfo(client, name)
		if err == nil && info.State == state {
			return
		}
		if err == nil {
			lastState = info.State
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for process %s to reach %s; last state was %s", name, state, lastState)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// getProcessInfo fetches structured process info via the JSON status API.
func getProcessInfo(client *ctl.Client, name string) (processInfo, error) {
	var buf bytes.Buffer
	err := client.StatusWithOptions([]string{name}, ctl.StatusOptions{JSON: true}, &buf)
	if err != nil {
		return processInfo{}, err
	}
	var infos []processInfo
	if err := json.Unmarshal(buf.Bytes(), &infos); err != nil {
		return processInfo{}, fmt.Errorf("parse status JSON: %w (raw: %s)", err, buf.String())
	}
	if len(infos) == 0 {
		return processInfo{}, fmt.Errorf("no process info returned for %s", name)
	}
	return infos[0], nil
}

// writeScript creates an executable shell script in dir and returns its path.
// Use this instead of "/bin/sh -c '...'" since kahi uses strings.Fields for
// command tokenization (no shell quoting support).
func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+content+"\n"), 0755); err != nil {
		t.Fatalf("write script %s: %v", name, err)
	}
	return path
}

// getAllProcessInfo fetches info for all processes.
func getAllProcessInfo(client *ctl.Client) ([]processInfo, error) {
	var buf bytes.Buffer
	err := client.StatusWithOptions(nil, ctl.StatusOptions{JSON: true}, &buf)
	if err != nil {
		return nil, err
	}
	var infos []processInfo
	if err := json.Unmarshal(buf.Bytes(), &infos); err != nil {
		return nil, fmt.Errorf("parse status JSON: %w", err)
	}
	return infos, nil
}
