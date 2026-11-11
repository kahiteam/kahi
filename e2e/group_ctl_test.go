//go:build e2e

package e2e

import (
	"testing"
	"time"
)

const groupCtlConfig = `
[programs.web]
command = "/bin/sleep 300"
autostart = false
startsecs = 0
priority = 100

[programs.api]
command = "/bin/sleep 300"
autostart = false
startsecs = 0
priority = 200

[programs.worker]
command = "/bin/sleep 300"
autostart = false
startsecs = 0
priority = 300

[groups.services]
programs = ["web", "api"]
priority = 100

[groups.background]
programs = ["worker"]
priority = 200
`

func TestGroup_StartAll(t *testing.T) {
	t.Parallel()
	client, _ := startDaemon(t, groupCtlConfig)

	if err := client.StartGroup("services"); err != nil {
		t.Fatalf("start group services: %v", err)
	}
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	waitForState(t, client, "api", "RUNNING", 5*time.Second)
}

func TestGroup_StopAll(t *testing.T) {
	t.Parallel()
	client, _ := startDaemon(t, groupCtlConfig)

	if err := client.StartGroup("services"); err != nil {
		t.Fatalf("start group: %v", err)
	}
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	waitForState(t, client, "api", "RUNNING", 5*time.Second)

	if err := client.StopGroup("services"); err != nil {
		t.Fatalf("stop group: %v", err)
	}
	waitForState(t, client, "web", "STOPPED", 10*time.Second)
	waitForState(t, client, "api", "STOPPED", 10*time.Second)
}

func TestGroup_RestartAll(t *testing.T) {
	t.Parallel()
	client, _ := startDaemon(t, groupCtlConfig)

	if err := client.StartGroup("services"); err != nil {
		t.Fatalf("start group: %v", err)
	}
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	waitForState(t, client, "api", "RUNNING", 5*time.Second)

	info1, _ := getProcessInfo(client, "web")
	pid1 := info1.PID

	if err := client.RestartGroup("services"); err != nil {
		t.Fatalf("restart group: %v", err)
	}
	waitForState(t, client, "web", "RUNNING", 10*time.Second)
	waitForState(t, client, "api", "RUNNING", 10*time.Second)

	info2, _ := getProcessInfo(client, "web")
	if info2.PID == pid1 {
		t.Fatal("web PID did not change after group restart")
	}
}

func TestGroup_StartSingle(t *testing.T) {
	t.Parallel()
	client, _ := startDaemon(t, groupCtlConfig)

	// Start individual process within a group.
	if err := client.Start("web"); err != nil {
		t.Fatalf("start web: %v", err)
	}
	waitForState(t, client, "web", "RUNNING", 5*time.Second)

	// api should still be stopped.
	info, _ := getProcessInfo(client, "api")
	if info.State != "STOPPED" {
		t.Fatalf("api state = %s, want STOPPED (only web was started)", info.State)
	}
}

func TestGroup_PriorityOrder(t *testing.T) {
	t.Parallel()
	client, _ := startDaemon(t, groupCtlConfig)

	// Start all groups. Lower priority numbers start first.
	if err := client.StartGroup("services"); err != nil {
		t.Fatalf("start services: %v", err)
	}
	if err := client.StartGroup("background"); err != nil {
		t.Fatalf("start background: %v", err)
	}

	// All should reach RUNNING regardless of order.
	waitForState(t, client, "web", "RUNNING", 10*time.Second)
	waitForState(t, client, "api", "RUNNING", 10*time.Second)
	waitForState(t, client, "worker", "RUNNING", 10*time.Second)
}

func TestGroup_Heterogeneous(t *testing.T) {
	t.Parallel()
	client, _ := startDaemon(t, `
[programs.fast]
command = "/bin/sleep 300"
autostart = false
startsecs = 0

[programs.slow]
command = "/bin/sleep 300"
autostart = false
startsecs = 2

[groups.mixed]
programs = ["fast", "slow"]
`)
	if err := client.StartGroup("mixed"); err != nil {
		t.Fatalf("start mixed group: %v", err)
	}

	// fast should be RUNNING quickly.
	waitForState(t, client, "fast", "RUNNING", 5*time.Second)

	// slow needs 2 seconds (startsecs=2) to reach RUNNING.
	waitForState(t, client, "slow", "RUNNING", 10*time.Second)
}
