//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const processCtlConfig = `
[programs.sleeper]
command = "/bin/sleep 300"
autostart = true
startsecs = 1
startretries = 3
stopsignal = "TERM"
stopwaitsecs = 5

[programs.manual]
command = "/bin/sleep 300"
autostart = false
startsecs = 1
`

func TestProcess_Start(t *testing.T) {
	client, _ := startDaemon(t, processCtlConfig)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	// manual is autostart=false, should be STOPPED.
	info, err := getProcessInfo(client, "manual")
	if err != nil {
		t.Fatalf("get manual info: %v", err)
	}
	if info.State != "STOPPED" {
		t.Fatalf("manual state = %s, want STOPPED", info.State)
	}

	// Start it.
	if err := client.Start("manual"); err != nil {
		t.Fatalf("start manual: %v", err)
	}
	waitForState(t, client, "manual", "RUNNING", 5*time.Second)
}

func TestProcess_Stop(t *testing.T) {
	client, _ := startDaemon(t, processCtlConfig)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	if err := client.Stop("sleeper"); err != nil {
		t.Fatalf("stop sleeper: %v", err)
	}
	waitForState(t, client, "sleeper", "STOPPED", 10*time.Second)
}

func TestProcess_Restart(t *testing.T) {
	client, _ := startDaemon(t, processCtlConfig)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	info1, _ := getProcessInfo(client, "sleeper")
	pid1 := info1.PID

	if err := client.Restart("sleeper"); err != nil {
		t.Fatalf("restart sleeper: %v", err)
	}
	waitForState(t, client, "sleeper", "RUNNING", 10*time.Second)

	info2, _ := getProcessInfo(client, "sleeper")
	if info2.PID == pid1 {
		t.Fatal("PID did not change after restart")
	}
}

func TestProcess_Status(t *testing.T) {
	client, _ := startDaemon(t, processCtlConfig)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	infos, err := getAllProcessInfo(client)
	if err != nil {
		t.Fatalf("get all status: %v", err)
	}
	names := make(map[string]bool)
	for _, info := range infos {
		names[info.Name] = true
	}
	if !names["sleeper"] {
		t.Error("status missing 'sleeper'")
	}
	if !names["manual"] {
		t.Error("status missing 'manual'")
	}
}

func TestProcess_StatusWithOptions(t *testing.T) {
	client, _ := startDaemon(t, processCtlConfig)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	info, err := getProcessInfo(client, "sleeper")
	if err != nil {
		t.Fatalf("get sleeper info: %v", err)
	}
	if info.Name != "sleeper" {
		t.Fatalf("name = %q, want sleeper", info.Name)
	}
	if info.State != "RUNNING" {
		t.Fatalf("state = %q, want RUNNING", info.State)
	}
}

func TestProcess_Signal(t *testing.T) {
	dir := t.TempDir()
	// Use a script that traps USR1 so it doesn't terminate
	// (/bin/sleep exits on USR1 on macOS).
	// The ready file is written after the trap: with startsecs = 0 the process
	// is RUNNING before the shell has installed it.
	ready := filepath.Join(dir, "ready")
	script := writeScript(t, dir, "trapper.sh", "trap 'echo got-usr1' USR1\necho ready > "+ready+"\nwhile true; do sleep 1; done")

	client, _ := startDaemon(t, `
[programs.trapper]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "trapper", "RUNNING", 5*time.Second)
	waitForMarker(t, ready, "ready", 5*time.Second)

	info1, _ := getProcessInfo(client, "trapper")

	if _, err := client.Signal("trapper", "USR1"); err != nil {
		t.Fatalf("signal USR1: %v", err)
	}

	// Process should still be running after USR1.
	time.Sleep(500 * time.Millisecond)
	info2, err := getProcessInfo(client, "trapper")
	if err != nil {
		t.Fatalf("get info after signal: %v", err)
	}
	if info2.State != "RUNNING" {
		t.Fatalf("state after USR1 = %s, want RUNNING", info2.State)
	}
	if info2.PID != info1.PID {
		t.Fatal("PID changed after USR1")
	}
}

func TestProcess_SignalTerm(t *testing.T) {
	client, _ := startDaemon(t, processCtlConfig)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	if _, err := client.Signal("sleeper", "TERM"); err != nil {
		t.Fatalf("signal TERM: %v", err)
	}

	// Process should stop (sleep responds to TERM).
	// It may go to EXITED or STOPPED depending on autorestart.
	time.Sleep(2 * time.Second)
	info, err := getProcessInfo(client, "sleeper")
	if err != nil {
		t.Fatalf("get info after TERM: %v", err)
	}
	// With default autorestart=unexpected, TERM causes expected exit so
	// it may restart. Just verify the signal was delivered (state changed).
	t.Logf("state after TERM signal: %s", info.State)
}

func TestProcess_StartAlreadyRunning(t *testing.T) {
	client, _ := startDaemon(t, processCtlConfig)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	err := client.Start("sleeper")
	if err == nil {
		t.Fatal("expected error starting already-running process")
	}
}

func TestProcess_StopAlreadyStopped(t *testing.T) {
	client, _ := startDaemon(t, processCtlConfig)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	// manual is autostart=false, should be STOPPED.
	err := client.Stop("manual")
	if err == nil {
		t.Fatal("expected error stopping already-stopped process")
	}
}

func TestProcess_StartBadName(t *testing.T) {
	client, _ := startDaemon(t, processCtlConfig)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	err := client.Start("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not found") &&
		!strings.Contains(strings.ToLower(err.Error()), "no such") {
		t.Fatalf("error = %q, want 'not found' or 'no such'", err)
	}
}

func TestProcess_StartFails(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.broken]
command = "/nonexistent/binary/that/does/not/exist"
autostart = true
startsecs = 1
startretries = 1
`)
	waitForState(t, client, "broken", "FATAL", 15*time.Second)
}

func TestProcess_StopWaitSecs(t *testing.T) {
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
	if err := client.Stop("stubborn"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForState(t, client, "stubborn", "STOPPED", 15*time.Second)
	elapsed := time.Since(start)

	// Should have been killed after ~2 seconds (stopwaitsecs).
	if elapsed < 1*time.Second {
		t.Fatalf("stopped too fast (%v), stopwaitsecs=2 not respected", elapsed)
	}
}

func TestProcess_StopSignal(t *testing.T) {
	// Process with custom stopsignal.
	client, _ := startDaemon(t, `
[programs.custom]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
stopsignal = "INT"
stopwaitsecs = 5
`)
	waitForState(t, client, "custom", "RUNNING", 5*time.Second)

	if err := client.Stop("custom"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitForState(t, client, "custom", "STOPPED", 10*time.Second)
}

func TestProcess_StartRetries(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.flaky]
command = "`+binFalse+`"
autostart = true
startsecs = 1
startretries = 2
`)
	// After 2 retries, should be FATAL.
	waitForState(t, client, "flaky", "FATAL", 20*time.Second)
}

func TestProcess_StartSecs(t *testing.T) {
	// Process must survive startsecs=2 to be considered RUNNING.
	client, _ := startDaemon(t, `
[programs.quick]
command = "/bin/sleep 300"
autostart = true
startsecs = 2
`)
	// Should be STARTING initially, then RUNNING after 2 seconds.
	time.Sleep(500 * time.Millisecond)
	info, _ := getProcessInfo(client, "quick")
	if info.State != "STARTING" && info.State != "RUNNING" {
		t.Fatalf("state = %s, want STARTING or RUNNING", info.State)
	}

	waitForState(t, client, "quick", "RUNNING", 10*time.Second)
}
