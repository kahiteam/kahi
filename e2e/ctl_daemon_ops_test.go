//go:build e2e

package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kahi ctl daemon operations, run as the binary (FUNC-036, #148).

// ctl shutdown returns only once the daemon has exited.
func TestCtl_ShutdownWaits(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	// Ignores TERM, so the shutdown takes stopwaitsecs (2s) to KILL it.
	script := writeScript(t, dir, "stubborn.sh", "trap '' TERM\necho ready > "+ready+"\nexec sleep 300")
	_, _, done, _, socket := shutdownDaemon(t, 10, fmt.Sprintf(`
[programs.worker]
command = %q
autostart = true
startsecs = 0
stopwaitsecs = 2
`, script))
	waitForMarker(t, ready, "ready", 5*time.Second)

	start := time.Now()
	out, stderr, code := runKahi(t, dir, "ctl", "-s", socket, "shutdown")
	elapsed := time.Since(start)
	if code != 0 || out != "shutdown initiated\nshut down\n" {
		t.Fatalf("ctl shutdown: exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	select {
	case <-done:
	default:
		t.Fatal("ctl shutdown returned while the daemon was still running")
	}
	if elapsed < 2*time.Second {
		t.Errorf("ctl shutdown returned after %v, before the 2s stop of the worker", elapsed)
	}

	// No daemon: a connection error, exit 1.
	_, stderr, code = runKahi(t, dir, "ctl", "-s", socket, "shutdown")
	if code != 1 || !strings.Contains(stderr, "connection failed") {
		t.Fatalf("ctl shutdown without a daemon: exit %d, stderr %q", code, stderr)
	}
}

func TestCtl_ReloadVersionPID(t *testing.T) {
	client, socket := startDaemon(t, `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0

[programs.api]
command = "/bin/sleep 300"
startsecs = 0

[programs.idle]
command = "/bin/sleep 300"
autostart = false
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	waitForState(t, client, "api", "RUNNING", 5*time.Second)
	web, _ := getProcessInfo(client, "web")
	api, _ := getProcessInfo(client, "api")
	dir := t.TempDir()
	ctl := func(args ...string) string {
		t.Helper()
		out, stderr, code := runKahi(t, dir, append([]string{"ctl", "-s", socket}, args...)...)
		if code != 0 {
			t.Fatalf("ctl %v: exit %d, stderr %q", args, code, stderr)
		}
		return out
	}

	if got := strings.TrimSpace(ctl("pid")); got != fmt.Sprint(daemonPID(t, client)) {
		t.Errorf("ctl pid = %q, want the daemon pid %d", got, daemonPID(t, client))
	}
	if got := strings.TrimSpace(ctl("pid", "web")); got != fmt.Sprint(web.PID) {
		t.Errorf("ctl pid web = %q, want %d", got, web.PID)
	}
	// Running processes only, by name.
	if got, want := ctl("pid", "all"), fmt.Sprintf("api %d\nweb %d\n", api.PID, web.PID); got != want {
		t.Errorf("ctl pid all = %q, want %q", got, want)
	}

	if out := ctl("version"); !strings.Contains(out, "version: ") || !strings.Contains(out, fmt.Sprintf("pid: %d", daemonPID(t, client))) {
		t.Errorf("ctl version = %q", out)
	}

	rewriteConfig(t, socket, `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0

[programs.api]
command = "/bin/sleep 300"
startsecs = 0

[programs.extra]
command = "/bin/sleep 300"
autostart = false
`)
	if out := ctl("reload"); out != "config reloaded: added=[extra] changed=[] removed=[idle]\n" {
		t.Errorf("ctl reload = %q", out)
	}
	if _, err := getProcessInfo(client, "extra"); err != nil {
		t.Errorf("extra not loaded after reload: %v", err)
	}
}
