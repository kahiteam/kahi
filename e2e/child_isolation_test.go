//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// Child process isolation (FUNC-016): each child runs in its own process
// group, so a signal to the daemon's group does not reach it.

// A SIGTERM to the daemon's process group reaches the daemon, not the child:
// the child sees only its configured stop signal, INT, during the shutdown.
func TestChildIsolation_GroupSignalNotDelivered(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	sigs := filepath.Join(dir, "sigs")
	script := writeScript(t, dir, "child.sh",
		"trap 'echo TERM >> "+sigs+"' TERM\ntrap 'echo INT >> "+sigs+"; exit 0' INT\necho ready > "+filepath.Join(dir, "ready")+"\nwhile true; do sleep 0.1; done")
	socket := filepath.Join(dir, "kahi.sock")
	config := filepath.Join(dir, "kahi.toml")
	cfg := "[supervisor]\nshutdown_timeout = 10\n\n[server.unix]\nfile = \"" + socket + "\"\n\n" +
		"[programs.child]\ncommand = \"" + script + "\"\nstartsecs = 0\nstopsignal = \"INT\"\nstopwaitsecs = 5\n"
	if err := os.WriteFile(config, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// The daemon leads its own process group, so the group signal below
	// cannot reach the test binary.
	cmd := exec.Command(kahiBinary, "daemon", "-c", config)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out := daemonOutput(t)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	waitForSocket(t, socket, 5*time.Second)
	client := ctl.NewUnixClient(socket)
	waitForHealth(t, client, 3*time.Second)
	waitForState(t, client, "child", "RUNNING", 5*time.Second)
	waitForMarker(t, filepath.Join(dir, "ready"), "ready", 5*time.Second)

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if pgid != cmd.Process.Pid {
		t.Fatalf("daemon pgid %d, want its own pid %d", pgid, cmd.Process.Pid)
	}
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		t.Fatalf("kill -TERM -%d: %v", pgid, err)
	}
	select {
	case <-exited:
		exited <- nil // for the cleanup
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not exit within 10s of the group SIGTERM")
	}

	b, _ := os.ReadFile(sigs)
	if got := strings.Fields(string(b)); len(got) != 1 || got[0] != "INT" {
		t.Fatalf("child received %v, want only INT (its stopsignal), not the group TERM", got)
	}
}

// A program whose directory does not exist fails to spawn on every attempt:
// it goes STARTING, BACKOFF, STARTING, FATAL, and its status says why.
func TestChildIsolation_SpawnErrorBackoffToFatal(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing")
	client, socket := startDaemon(t, `
[programs.nodir]
command = "/bin/sleep 300"
directory = "`+missing+`"
autostart = false
startsecs = 1
startretries = 1
`)
	next := stateEvents(t, socket, "nodir")
	_ = client.Start("nodir")
	wantState(t, next(5*time.Second), "STARTING", "STOPPED")
	wantState(t, next(5*time.Second), "BACKOFF", "STARTING")
	wantState(t, next(5*time.Second), "STARTING", "BACKOFF")
	wantState(t, next(5*time.Second), "FATAL", "STARTING")

	info, err := getProcessInfo(client, "nodir")
	if err != nil {
		t.Fatal(err)
	}
	if want := "spawn error: directory " + missing + " does not exist"; info.State != "FATAL" || info.Description != want {
		t.Fatalf("nodir = %s %q, want FATAL %q", info.State, info.Description, want)
	}
}
