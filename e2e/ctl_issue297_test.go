//go:build e2e

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// kahi ctl tail -f no longer ends silently when the stream ends: it
// reconnects, and when the daemon is gone it exits 1 with "log stream
// lost" (#297).
func TestCtl_TailFollowDaemonGone(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	socket := filepath.Join(dir, "kahi.sock")
	script := writeScript(t, dir, "tick.sh", "while :; do echo tick; sleep 0.2; done\n")
	cfgPath := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(cfgPath, []byte("[server.unix]\nfile = \""+socket+"\"\n\n[programs.t]\ncommand = \""+script+"\"\nstartsecs = 0\nstopwaitsecs = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	daemon := startDaemonCmd(t, kahiBinary, cfgPath, dir)
	waitForSocket(t, socket, 5*time.Second)
	waitForState(t, ctl.NewUnixClient(socket), "t", "RUNNING", 5*time.Second)

	var out, errb syncWriter
	tail := exec.Command(kahiBinary, "ctl", "-s", socket, "tail", "-f", "t")
	tail.Stdout, tail.Stderr = &out, &errb
	if err := tail.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- tail.Wait() }()
	t.Cleanup(func() { _ = tail.Process.Kill() })
	waitText(t, &out, "tick")

	stopDaemon(t, daemon, socket)
	select {
	case err := <-done:
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 || !strings.Contains(errb.String(), "log stream lost") {
			t.Errorf("tail -f after the daemon stopped: %v, stderr %q; want exit 1 and 'log stream lost'", err, errb.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("tail -f did not end after the daemon stopped")
	}
}

// ctl pid reports the API's error: over TCP without credentials that is
// "authentication required", not "no such process" (#297).
func TestCtl_PIDReportsAuthError(t *testing.T) {
	t.Parallel()
	d := startWebUIDaemon(t, false)
	_, stderr, code := runKahi(t, t.TempDir(), "ctl", "--addr", strings.TrimPrefix(d.base, "http://"), "pid", "sleeper")
	if code != 1 || !strings.Contains(stderr, "authentication required") || strings.Contains(stderr, "no such process") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}
