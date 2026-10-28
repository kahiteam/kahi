//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Running as an ordinary user (FUNC-044, #146).

// Without XDG_RUNTIME_DIR the socket goes into a kahi-<uid> directory under
// the temp dir, which the daemon creates owner-only; a directory of that name
// that group or others can reach is refused.
func TestUnprivileged_DefaultSocketDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root defaults to /var/run/kahi.sock")
	}
	tmp := shortTempDir(t)
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", tmp)
	sockDir := filepath.Join(tmp, fmt.Sprintf("kahi-%d", os.Geteuid()))
	cfgPath := filepath.Join(tmp, "kahi.toml")
	if err := os.WriteFile(cfgPath, []byte("[programs.web]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := startDaemonCmd(t, kahiBinary, cfgPath, tmp)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForSocket(t, filepath.Join(sockDir, "kahi.sock"), 5*time.Second)
	if fi, err := os.Stat(sockDir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("socket dir %v, %v; want mode 0700", fi, err)
	}
	out, stderr, code := runKahi(t, tmp, "ctl", "-c", cfgPath, "status", "web")
	if code != 0 || !strings.Contains(out, "web ") {
		t.Fatalf("ctl status without -s: exit %d, %q, stderr %q", code, out, stderr)
	}
	if _, _, code := runKahi(t, tmp, "ctl", "-c", cfgPath, "shutdown"); code != 0 {
		t.Fatal("ctl shutdown failed")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not exit")
	}

	// A directory others can reach is refused.
	if err := os.Chmod(sockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = runKahi(t, tmp, "daemon", "-c", cfgPath)
	if code == 0 || !strings.Contains(stderr, fmt.Sprintf("socket directory %s must be a directory owned by uid %d with mode 0700", sockDir, os.Geteuid())) {
		t.Fatalf("daemon with a 0755 socket dir: exit %d, stderr %q", code, stderr)
	}
}

// A socket path the user cannot write to stops the daemon with the reason.
func TestUnprivileged_UnwritableSocketPathRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := shortTempDir(t)
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	cfgPath := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf("[server.unix]\nfile = %q\n", filepath.Join(locked, "kahi.sock"))), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runKahi(t, dir, "daemon", "-c", cfgPath)
	if code == 0 || !strings.Contains(stderr, "permission denied: cannot create socket in "+locked) {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}
