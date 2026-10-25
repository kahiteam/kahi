package process

import (
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
)

func spawnCount(s *MockSpawner) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.SpawnCalls)
}

// backoffConfig makes an early exit land in BACKOFF rather than RUNNING.
func backoffConfig() config.ProgramConfig {
	cfg := defaultProgramConfig()
	cfg.Startsecs = 10
	cfg.Startretries = 3
	return cfg
}

func TestStopAllStopsBackoffProcess(t *testing.T) {
	spawner := &MockSpawner{}
	mgr := NewManager(spawner, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{Programs: map[string]config.ProgramConfig{"worker": backoffConfig()}})
	p := mgr.Processes()[0]

	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	p.HandleExit(1)
	if p.State() != Backoff {
		t.Fatalf("state = %s, want BACKOFF", p.State())
	}

	mgr.StopAll()
	if p.State() != Stopped {
		t.Fatalf("state after StopAll = %s, want STOPPED", p.State())
	}

	// The first backoff delay is 1s; no retry may spawn after StopAll.
	time.Sleep(1200 * time.Millisecond)
	if n := spawnCount(spawner); n != 1 {
		t.Fatalf("spawned %d times, want 1 (respawned during shutdown)", n)
	}
}

// A reload removes a group with StopGroup + RemoveGroup; a process in
// BACKOFF must not respawn as an untracked child afterwards.
func TestStopGroupStopsBackoffProcess(t *testing.T) {
	spawner := &MockSpawner{}
	mgr := NewManager(spawner, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{Programs: map[string]config.ProgramConfig{"worker": backoffConfig()}})
	p := mgr.Processes()[0]

	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	p.HandleExit(1)
	if p.State() != Backoff {
		t.Fatalf("state = %s, want BACKOFF", p.State())
	}

	if err := mgr.StopGroup("worker"); err != nil {
		t.Fatalf("stop group: %v", err)
	}
	if p.State() != Stopped {
		t.Fatalf("state after StopGroup = %s, want STOPPED", p.State())
	}
	if err := mgr.RemoveGroup("worker"); err != nil {
		t.Fatalf("remove group: %v", err)
	}

	time.Sleep(1200 * time.Millisecond)
	if n := spawnCount(spawner); n != 1 {
		t.Fatalf("spawned %d times, want 1 (removed process respawned)", n)
	}
}

func TestBackoffRetrySkippedAfterShutdown(t *testing.T) {
	shutdown := make(chan struct{})
	spawner := &MockSpawner{}
	p := NewProcess("worker", "worker", backoffConfig(), spawner, testBus(), testLogger(), WithShutdownCh(shutdown))

	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	p.HandleExit(1)
	if p.State() != Backoff {
		t.Fatalf("state = %s, want BACKOFF", p.State())
	}

	// Shutdown begins while the retry timer is pending.
	close(shutdown)
	time.Sleep(1200 * time.Millisecond)

	if n := spawnCount(spawner); n != 1 {
		t.Fatalf("spawned %d times, want 1 (retried during shutdown)", n)
	}
	if p.State() != Stopped {
		t.Fatalf("state = %s, want STOPPED", p.State())
	}
}

func TestEarlyExitDuringShutdownStops(t *testing.T) {
	shutdown := make(chan struct{})
	spawner := &MockSpawner{}
	p := NewProcess("worker", "worker", backoffConfig(), spawner, testBus(), testLogger(), WithShutdownCh(shutdown))

	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	close(shutdown)
	p.HandleExit(1)

	if p.State() != Stopped {
		t.Fatalf("state = %s, want STOPPED instead of BACKOFF during shutdown", p.State())
	}
}

func TestAutorestartSkippedAfterShutdown(t *testing.T) {
	shutdown := make(chan struct{})
	cfg := defaultProgramConfig()
	cfg.Autorestart = "false"
	spawner := &MockSpawner{}
	p := NewProcess("worker", "worker", cfg, spawner, testBus(), testLogger(), WithShutdownCh(shutdown))

	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	p.HandleExit(1)
	if p.State() != Exited {
		t.Fatalf("state = %s, want EXITED", p.State())
	}

	// A restart queued before shutdown runs after it began.
	close(shutdown)
	p.restartAfterExit()

	if n := spawnCount(spawner); n != 1 {
		t.Fatalf("spawned %d times, want 1 (autorestarted during shutdown)", n)
	}
	if p.State() != Exited {
		t.Fatalf("state = %s, want EXITED", p.State())
	}
}
