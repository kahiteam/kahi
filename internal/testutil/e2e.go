//go:build e2e

package testutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// E2EDaemon holds a reference to a real kahi binary process for E2E testing.
type E2EDaemon struct {
	Cmd        *exec.Cmd
	SocketPath string
	Dir        string
	ConfigPath string
	cancel     context.CancelFunc
}

// DefaultE2ETimeout is the maximum time an E2E test should run.
const DefaultE2ETimeout = 30 * time.Second

// StartE2EDaemon builds and starts a real Kahi daemon for end-to-end testing.
// The daemon runs in an isolated temp directory with its own config and socket.
func StartE2EDaemon(t *testing.T, configTOML string) *E2EDaemon {
	t.Helper()

	// Locate the kahi binary -- prefer ./bin/kahi, fall back to go run.
	binary := findKahiBinary(t)

	dir := TempDir(t)
	socketPath := fmt.Sprintf("%s/kahi-e2e-%d.sock", dir, time.Now().UnixNano())

	fullConfig := fmt.Sprintf(`
[supervisor]
log_level = "debug"
log_format = "text"

[server.unix]
file = %q

%s
`, socketPath, configTOML)

	configPath := WriteFile(t, dir, "kahi.toml", fullConfig)

	ctx, cancel := context.WithTimeout(context.Background(), DefaultE2ETimeout)

	cmd := exec.CommandContext(ctx, binary, "daemon", "--config", configPath)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("cannot start e2e daemon: %v", err)
	}

	t.Cleanup(func() {
		cancel()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(dir)
	})

	return &E2EDaemon{
		Cmd:        cmd,
		SocketPath: socketPath,
		Dir:        dir,
		ConfigPath: configPath,
		cancel:     cancel,
	}
}

func findKahiBinary(t *testing.T) string {
	t.Helper()
	// Check for pre-built binary.
	candidates := []string{"./bin/kahi", "bin/kahi", "kahi"}
	for _, c := range candidates {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	// Fall back to go run.
	return "go"
}
