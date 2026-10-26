//go:build e2e

package e2e

import (
	"testing"
	"time"
)

func TestState_AutorestartTrue(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.shortlived]
command = "`+binFalse+`"
autostart = true
autorestart = "true"
startsecs = 0
startretries = 3
`)
	// Process exits immediately, should be restarted because autorestart=true.
	// Wait a bit for at least one restart cycle.
	time.Sleep(2 * time.Second)
	info, err := getProcessInfo(client, "shortlived")
	if err != nil {
		t.Fatalf("get info: %v", err)
	}
	// Should be in STARTING, RUNNING, or BACKOFF (actively restarting).
	if info.State == "STOPPED" || info.State == "EXITED" {
		t.Fatalf("state = %s, want active restart cycle (autorestart=true)", info.State)
	}
}

func TestState_AutorestartFalse(t *testing.T) {
	dir := t.TempDir()
	// Script must survive past startsecs (defaults to 1) to reach RUNNING,
	// then exit cleanly to test autorestart="false".
	script := writeScript(t, dir, "oneshot.sh", "sleep 2\nexit 0")

	client, _ := startDaemon(t, `
[programs.oneshot]
command = "`+script+`"
autostart = true
autorestart = "false"
`)
	// Wait for process to reach RUNNING (after startsecs=1 default).
	waitForState(t, client, "oneshot", "RUNNING", 5*time.Second)

	// Process exits after 2 seconds and should NOT restart.
	waitForState(t, client, "oneshot", "EXITED", 10*time.Second)

	// Wait a bit more and verify it stays EXITED.
	time.Sleep(1 * time.Second)
	info, _ := getProcessInfo(client, "oneshot")
	if info.State != "EXITED" {
		t.Fatalf("state = %s, want EXITED (autorestart=false)", info.State)
	}
}

func TestState_AutorestartUnexpected(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.unexpected]
command = "`+binFalse+`"
autostart = true
autorestart = "unexpected"
startsecs = 1
exitcodes = [0]
startretries = 3
`)
	// startsecs = 1 makes each exit a failed start, so retries run out and
	// the program goes FATAL. With startsecs = 0 the program counts as
	// started and autorestart would restart it forever, as in supervisord.
	waitForState(t, client, "unexpected", "FATAL", 20*time.Second)
}

func TestState_BackoffToFatal(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.failing]
command = "`+binFalse+`"
autostart = true
startsecs = 1
startretries = 2
`)
	// Process exits immediately (before startsecs=1), triggers backoff.
	// After 2 retries, should reach FATAL.
	waitForState(t, client, "failing", "FATAL", 20*time.Second)
}

func TestState_ExpectedExitCode(t *testing.T) {
	dir := t.TempDir()
	// Script must survive past startsecs (defaults to 1) to reach RUNNING.
	script := writeScript(t, dir, "exit2.sh", "sleep 2\nexit 2")

	client, _ := startDaemon(t, `
[programs.expected]
command = "`+script+`"
autostart = true
autorestart = "unexpected"
exitcodes = [0, 2]
`)
	// Wait for process to reach RUNNING.
	waitForState(t, client, "expected", "RUNNING", 5*time.Second)

	// Exit code 2 is in exitcodes=[0,2], so it's expected. Should not restart.
	waitForState(t, client, "expected", "EXITED", 10*time.Second)

	time.Sleep(1 * time.Second)
	info, _ := getProcessInfo(client, "expected")
	if info.State != "EXITED" {
		t.Fatalf("state = %s, want EXITED (exit code 2 is expected)", info.State)
	}
}

func TestState_UnexpectedExitCode(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.unexp]
command = "`+binFalse+`"
autostart = true
autorestart = "unexpected"
startsecs = 1
exitcodes = [0]
startretries = 2
`)
	// Exit code 1 is not in exitcodes=[0], so it's unexpected.
	// With autorestart=unexpected, it should retry then go FATAL.
	waitForState(t, client, "unexp", "FATAL", 20*time.Second)
}

func TestState_KilledDuringBackoff(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.backoff]
command = "`+binFalse+`"
autostart = true
startsecs = 1
startretries = 10
`)
	// Wait for process to enter BACKOFF.
	waitForState(t, client, "backoff", "BACKOFF", 10*time.Second)

	// Stop during BACKOFF should work.
	if err := client.Stop("backoff"); err != nil {
		t.Fatalf("stop during backoff: %v", err)
	}
	waitForState(t, client, "backoff", "STOPPED", 5*time.Second)
}

func TestState_ConcurrentStartStop(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.concurrent]
command = "/bin/sleep 300"
autostart = false
startsecs = 0
`)
	// Rapid start/stop should not deadlock.
	for i := 0; i < 5; i++ {
		_ = client.Start("concurrent")
		time.Sleep(100 * time.Millisecond)
		_ = client.Stop("concurrent")
		time.Sleep(100 * time.Millisecond)
	}

	// Should be in a stable state (not hung).
	info, err := getProcessInfo(client, "concurrent")
	if err != nil {
		t.Fatalf("get info after concurrent ops: %v", err)
	}
	t.Logf("final state after concurrent start/stop: %s", info.State)
}

func TestState_NumprocsExpansion(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.worker]
command = "/bin/sleep 300"
process_name = "worker_%(process_num)d"
numprocs = 3
numprocs_start = 0
autostart = true
startsecs = 0
`)
	// Should create worker_0, worker_1, worker_2.
	for _, name := range []string{"worker_0", "worker_1", "worker_2"} {
		waitForState(t, client, name, "RUNNING", 10*time.Second)
	}
}

func TestState_Priority(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.low]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
priority = 100

[programs.high]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
priority = 999
`)
	// Both should be running. Priority affects start order but both should succeed.
	waitForState(t, client, "low", "RUNNING", 10*time.Second)
	waitForState(t, client, "high", "RUNNING", 10*time.Second)

	// Verify both are actually running.
	infos, err := getAllProcessInfo(client)
	if err != nil {
		t.Fatalf("get all info: %v", err)
	}
	running := 0
	for _, info := range infos {
		if info.State == "RUNNING" {
			running++
		}
	}
	if running < 2 {
		t.Fatalf("only %d processes running, want >= 2", running)
	}
}
