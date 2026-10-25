//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// rewriteConfig replaces the daemon's config with the same header startDaemon
// writes and the given programs.
func rewriteConfig(t *testing.T, socketPath, programs string) {
	t.Helper()
	cfg := fmt.Sprintf("[supervisor]\nlog_level = \"debug\"\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n\n%s", socketPath, programs)
	if err := os.WriteFile(filepath.Join(filepath.Dir(socketPath), "kahi.toml"), []byte(cfg), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// markerProgram returns a program whose process writes its first argument to
// marker at startup, so a test can tell which config the running process
// came from.
func markerProgram(t *testing.T, name, marker, version string) string {
	t.Helper()
	script := writeScript(t, t.TempDir(), "marker.sh", "echo \"$1\" > \"$2\"\nexec sleep 300")
	return fmt.Sprintf("[programs.%s]\ncommand = \"%s %s %s\"\nautostart = true\nstartsecs = 0\nstopwaitsecs = 2\n", name, script, version, marker)
}

// waitForMarker polls marker until it holds want.
func waitForMarker(t *testing.T, marker, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got string
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil {
			got = strings.TrimSpace(string(b))
			if got == want {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("marker = %q after %v, want %q", got, timeout, want)
}

func daemonPID(t *testing.T, client *ctl.Client) int {
	t.Helper()
	s, err := client.PID("")
	if err != nil {
		t.Fatalf("daemon pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("daemon pid %q: %v", s, err)
	}
	return pid
}

func TestReload_AddProgram(t *testing.T) {
	client, socketPath := startDaemon(t, `
[programs.existing]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "existing", "RUNNING", 5*time.Second)

	rewriteConfig(t, socketPath, `
[programs.existing]
command = "/bin/sleep 300"
autostart = true
startsecs = 0

[programs.newproc]
command = "/bin/sleep 300"
autostart = true
startsecs = 0

[programs.manual]
command = "/bin/sleep 300"
autostart = false
`)

	diff, err := client.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	t.Logf("reload diff: %v", diff)

	// An added program that autostarts is started by the reload itself.
	waitForState(t, client, "newproc", "RUNNING", 10*time.Second)

	// An added program with autostart = false is created but not started.
	info, err := getProcessInfo(client, "manual")
	if err != nil {
		t.Fatalf("manual not created by reload: %v", err)
	}
	if info.State != "STOPPED" {
		t.Fatalf("manual state = %s, want STOPPED (autostart = false)", info.State)
	}
}

func TestReload_RemoveProgram(t *testing.T) {
	client, socketPath := startDaemon(t, `
[programs.keeper]
command = "/bin/sleep 300"
autostart = true
startsecs = 0

[programs.goner]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
stopwaitsecs = 2
`)
	waitForState(t, client, "keeper", "RUNNING", 5*time.Second)
	waitForState(t, client, "goner", "RUNNING", 5*time.Second)
	keeper1, _ := getProcessInfo(client, "keeper")
	goner, _ := getProcessInfo(client, "goner")

	rewriteConfig(t, socketPath, `
[programs.keeper]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)

	diff, err := client.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if fmt.Sprint(diff["removed"]) != "[goner]" {
		t.Fatalf("reload removed = %v, want [goner]", diff["removed"])
	}

	if _, err := getProcessInfo(client, "goner"); err == nil {
		t.Fatal("goner still listed after it was removed from the config")
	}
	if err := syscall.Kill(goner.PID, 0); err != syscall.ESRCH {
		t.Fatalf("goner's child %d still running after removal (kill 0: %v)", goner.PID, err)
	}

	keeper2, _ := getProcessInfo(client, "keeper")
	if keeper2.State != "RUNNING" || keeper2.PID != keeper1.PID {
		t.Fatalf("keeper = %s pid %d, want RUNNING with unchanged pid %d", keeper2.State, keeper2.PID, keeper1.PID)
	}
}

// A changed program must restart with its new config (#104).
func TestReload_ChangeProgram(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	client, socketPath := startDaemon(t, markerProgram(t, "mutable", marker, "v1"))
	waitForState(t, client, "mutable", "RUNNING", 5*time.Second)
	waitForMarker(t, marker, "v1", 5*time.Second)

	rewriteConfig(t, socketPath, markerProgram(t, "mutable", marker, "v2"))
	diff, err := client.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if fmt.Sprint(diff["changed"]) != "[mutable]" {
		t.Fatalf("reload changed = %v, want [mutable]", diff["changed"])
	}

	waitForMarker(t, marker, "v2", 10*time.Second)
	waitForState(t, client, "mutable", "RUNNING", 10*time.Second)
}

// SIGHUP takes the same path as the API reload, without blocking the main
// loop that reaps the changed program's old process (#104).
func TestReload_SIGHUPChangeProgram(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	client, socketPath := startDaemon(t, markerProgram(t, "mutable", marker, "v1"))
	waitForState(t, client, "mutable", "RUNNING", 5*time.Second)
	waitForMarker(t, marker, "v1", 5*time.Second)

	rewriteConfig(t, socketPath, markerProgram(t, "mutable", marker, "v2"))
	if err := syscall.Kill(daemonPID(t, client), syscall.SIGHUP); err != nil {
		t.Fatalf("SIGHUP: %v", err)
	}

	waitForMarker(t, marker, "v2", 10*time.Second)
	waitForState(t, client, "mutable", "RUNNING", 10*time.Second)
}

// An environment-only change is a change too.
func TestReload_ChangeEnvironment(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	script := writeScript(t, t.TempDir(), "env.sh", "echo \"$VERSION\" > \"$1\"\nexec sleep 300")
	program := func(version string) string {
		return fmt.Sprintf("[programs.envy]\ncommand = \"%s %s\"\nautostart = true\nstartsecs = 0\nstopwaitsecs = 2\n\n[programs.envy.environment]\nVERSION = %q\n", script, marker, version)
	}
	client, socketPath := startDaemon(t, program("v1"))
	waitForMarker(t, marker, "v1", 5*time.Second)

	rewriteConfig(t, socketPath, program("v2"))
	if _, err := client.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	waitForMarker(t, marker, "v2", 10*time.Second)
	waitForState(t, client, "envy", "RUNNING", 10*time.Second)
}

// reread reports the diff and applies nothing (#105).
func TestReload_Reread(t *testing.T) {
	client, socketPath := startDaemon(t, `
[programs.stable]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "stable", "RUNNING", 5*time.Second)
	info1, _ := getProcessInfo(client, "stable")

	rewriteConfig(t, socketPath, `
[programs.stable]
command = "/bin/sleep 600"
autostart = true
startsecs = 0

[programs.preview]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)

	diff, err := client.Reread()
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if fmt.Sprint(diff["added"]) != "[preview]" || fmt.Sprint(diff["changed"]) != "[stable]" {
		t.Fatalf("reread diff = %v, want added [preview] and changed [stable]", diff)
	}
	if diff["status"] != "preview" {
		t.Fatalf("reread status = %v, want preview", diff["status"])
	}

	if _, err := getProcessInfo(client, "preview"); err == nil {
		t.Fatal("reread created program preview; it must not apply anything")
	}
	info2, _ := getProcessInfo(client, "stable")
	if info2.PID != info1.PID {
		t.Fatal("reread restarted stable; it must not apply anything")
	}

	// The same diff is still pending: a real reload applies it.
	diff, err = client.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if fmt.Sprint(diff["added"]) != "[preview]" {
		t.Fatalf("reload after reread added = %v, want [preview]", diff["added"])
	}
	waitForState(t, client, "preview", "RUNNING", 10*time.Second)
}

func TestReload_NoChange(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.static]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "static", "RUNNING", 5*time.Second)

	info1, _ := getProcessInfo(client, "static")

	diff, err := client.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	t.Logf("no-change reload diff: %v", diff)

	// Process should still be running with same PID.
	info2, _ := getProcessInfo(client, "static")
	if info2.PID != info1.PID {
		t.Fatal("PID changed after no-change reload")
	}
}

func TestReload_InvalidConfig(t *testing.T) {
	client, socketPath := startDaemon(t, `
[programs.safe]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "safe", "RUNNING", 5*time.Second)

	info1, _ := getProcessInfo(client, "safe")

	dir := filepath.Dir(socketPath)
	configPath := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(configPath, []byte("{{invalid toml"), 0644); err != nil {
		t.Fatalf("write bad config: %v", err)
	}

	_, err := client.Reload()
	if err == nil {
		t.Fatal("expected error reloading invalid config")
	}
	t.Logf("reload error (expected): %v", err)

	info2, _ := getProcessInfo(client, "safe")
	if info2.State != "RUNNING" {
		t.Fatalf("state = %s, want RUNNING after failed reload", info2.State)
	}
	if info2.PID != info1.PID {
		t.Fatal("PID changed after failed reload")
	}
}
