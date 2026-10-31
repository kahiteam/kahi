//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A process killed by a signal keeps the signal: status shows "killed by
// SIGKILL", --json and the API "exitsignal", with exitstatus -1. A signal
// death is unexpected, so autorestart = "unexpected" restarts it (#201).
func TestExitSignal_Reported(t *testing.T) {
	client, socket := startDaemon(t, `
[programs.victim]
command = "/bin/sleep 300"
startsecs = 0
autorestart = "false"

[programs.again]
command = "/bin/sleep 300"
startsecs = 0
autorestart = "unexpected"
`)
	waitForState(t, client, "victim", "RUNNING", 5*time.Second)
	waitForState(t, client, "again", "RUNNING", 5*time.Second)
	victim, _ := getProcessInfo(client, "victim")
	again, _ := getProcessInfo(client, "again")
	if err := syscall.Kill(victim.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(again.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "victim", "EXITED", 5*time.Second)

	out, _, code := runKahi(t, t.TempDir(), "ctl", "-s", socket, "status", "victim")
	if code != 0 || !strings.Contains(out, "killed by SIGKILL") {
		t.Errorf("status: exit %d, %q; want killed by SIGKILL", code, out)
	}
	out, _, _ = runKahi(t, t.TempDir(), "ctl", "-s", socket, "status", "--json", "victim")
	var procs []map[string]any
	if json.Unmarshal([]byte(out), &procs) != nil || len(procs) != 1 ||
		procs[0]["exitsignal"] != "SIGKILL" || procs[0]["exitstatus"] != float64(-1) {
		t.Errorf("status --json: %s; want exitsignal SIGKILL and exitstatus -1", out)
	}

	// The TERM death of "again" was unexpected: it runs again, new pid.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if info, _ := getProcessInfo(client, "again"); info.State == "RUNNING" && info.PID != again.PID {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	info, _ := getProcessInfo(client, "again")
	t.Fatalf("again after SIGTERM: %s pid %d (was %d), want restarted", info.State, info.PID, again.PID)
}
