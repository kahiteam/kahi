//go:build e2e

package testutil

import (
	"os"
	"testing"
)

func TestE2EFindBinary(t *testing.T) {
	binary := findKahiBinary(t)
	if binary == "" {
		t.Fatal("no binary found")
	}
}

func TestE2EDaemonDirSetup(t *testing.T) {
	dir := TempDir(t)
	socketPath := dir + "/kahi-e2e.sock"
	configPath := WriteFile(t, dir, "kahi.toml", `
[supervisor]
log_level = "debug"
`)

	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file does not exist: %v", err)
	}

	// Socket file should not exist before daemon starts.
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("socket should not exist yet")
	}
}
