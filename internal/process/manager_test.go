package process

import (
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
)

func TestManagerShutdownCh(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	ch := mgr.ShutdownCh()
	if ch == nil {
		t.Fatal("expected non-nil shutdown channel")
	}
}

func TestManagerProcessByPidFound(t *testing.T) {
	mp := NewMockProcess(5678)
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}
	mgr := NewManager(spawner, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	})

	// Start the process so it has a PID.
	if err := mgr.Start("web"); err != nil {
		t.Fatal(err)
	}

	found := mgr.ProcessByPid(5678)
	if found == nil {
		t.Fatal("expected to find process by PID")
	}
	if found.Name() != "web" {
		t.Fatalf("name = %q, want web", found.Name())
	}
}

func TestManagerProcessByPidNotFound(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	})

	found := mgr.ProcessByPid(99999)
	if found != nil {
		t.Fatal("expected nil for unknown PID")
	}
}

func TestManagerProcesses(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web":    defaultProgramConfig(),
			"worker": defaultProgramConfig(),
		},
	})

	procs := mgr.Processes()
	if len(procs) != 2 {
		t.Fatalf("expected 2 processes, got %d", len(procs))
	}
}

func TestManagerGroups(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web":    defaultProgramConfig(),
			"worker": defaultProgramConfig(),
		},
	})

	groups := mgr.Groups()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups["web"] == nil || groups["worker"] == nil {
		t.Fatal("missing expected group")
	}
}

func TestManagerAddGroup(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())

	if err := mgr.AddGroup("mygroup", []string{"web", "api"}, 100); err != nil {
		t.Fatal(err)
	}

	groups := mgr.Groups()
	if groups["mygroup"] == nil {
		t.Fatal("group not added")
	}
	if groups["mygroup"].Priority != 100 {
		t.Fatalf("priority = %d, want 100", groups["mygroup"].Priority)
	}
}

func TestManagerAddGroupDuplicate(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())

	if err := mgr.AddGroup("mygroup", []string{"web"}, 100); err != nil {
		t.Fatal(err)
	}
	err := mgr.AddGroup("mygroup", []string{"api"}, 200)
	if err == nil {
		t.Fatal("expected error for duplicate group")
	}
}

func TestManagerRemoveGroup(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	})

	// Process is in Stopped state, so removal should succeed.
	if err := mgr.RemoveGroup("web"); err != nil {
		t.Fatal(err)
	}

	groups := mgr.Groups()
	if groups["web"] != nil {
		t.Fatal("group should be removed")
	}

	// Process should also be removed.
	_, err := mgr.GetProcess("web")
	if err == nil {
		t.Fatal("process should be removed with group")
	}
}

func TestManagerRemoveGroupNonexistent(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())

	err := mgr.RemoveGroup("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent group")
	}
}

func TestManagerRemoveGroupRunningProcess(t *testing.T) {
	mp := NewMockProcess(1234)
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}
	mgr := NewManager(spawner, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	})

	if err := mgr.Start("web"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	err := mgr.RemoveGroup("web")
	if err == nil {
		t.Fatal("expected error when removing group with running process")
	}
}

func TestManagerAddRemoveProcess(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())

	p := NewProcess("dynamic", "dynamic", defaultProgramConfig(), &MockSpawner{}, testBus(), testLogger())
	mgr.AddProcess("dynamic", p)

	found, err := mgr.GetProcess("dynamic")
	if err != nil {
		t.Fatal(err)
	}
	if found.Name() != "dynamic" {
		t.Fatalf("name = %q, want dynamic", found.Name())
	}

	mgr.RemoveProcess("dynamic")
	_, err = mgr.GetProcess("dynamic")
	if err == nil {
		t.Fatal("expected error after removal")
	}
}

func TestManagerRemoveProcessNonexistent(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	// Should not panic.
	mgr.RemoveProcess("nonexistent")
}

func TestManagerList(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"zebra": defaultProgramConfig(),
			"alpha": defaultProgramConfig(),
		},
	})

	list := mgr.List()
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	// Should be sorted alphabetically.
	if list[0].Name != "alpha" {
		t.Fatalf("first = %q, want alpha", list[0].Name)
	}
	if list[1].Name != "zebra" {
		t.Fatalf("second = %q, want zebra", list[1].Name)
	}
}

func TestManagerGet(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	})

	info, err := mgr.Get("web")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "web" {
		t.Fatalf("name = %q, want web", info.Name)
	}
	if info.State != "STOPPED" {
		t.Fatalf("state = %q, want STOPPED", info.State)
	}

	_, err = mgr.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestManagerRestart(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	})

	// Restart a stopped process should just start it.
	if err := mgr.Restart("web"); err != nil {
		t.Fatal(err)
	}

	// Nonexistent returns error.
	if err := mgr.Restart("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestManagerSignalNonexistent(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	err := mgr.Signal("nonexistent", "TERM")
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestManagerWriteStdinNonexistent(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	err := mgr.WriteStdin("nonexistent", []byte("hello"))
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestManagerReadLog(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	})

	data, err := mgr.ReadLog("web", "stdout", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("expected empty data, got %d bytes", len(data))
	}

	_, err = mgr.ReadLog("nonexistent", "stdout", 0, 100)
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestManagerListGroups(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"zebra": defaultProgramConfig(),
			"alpha": defaultProgramConfig(),
		},
	})

	groups := mgr.ListGroups()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0] != "alpha" {
		t.Fatalf("first = %q, want alpha (sorted)", groups[0])
	}
}

func TestManagerStopGroup(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	})

	// Stop group with all stopped processes is a no-op.
	if err := mgr.StopGroup("web"); err != nil {
		t.Fatal(err)
	}

	err := mgr.StopGroup("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent group")
	}
}

func TestManagerRestartGroup(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	})

	if err := mgr.RestartGroup("web"); err != nil {
		t.Fatal(err)
	}

	err := mgr.RestartGroup("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent group")
	}
}

func TestManagerStopAll(t *testing.T) {
	spawner := &MockSpawner{}
	mgr := NewManager(spawner, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web":    defaultProgramConfig(),
			"worker": defaultProgramConfig(),
		},
	})

	// Start all.
	mgr.AutostartAll()
	time.Sleep(20 * time.Millisecond)

	// StopAll should close shutdownCh and send stop signals.
	mgr.StopAll()

	// ShutdownCh should be closed.
	select {
	case <-mgr.ShutdownCh():
	default:
		t.Fatal("shutdownCh should be closed after StopAll")
	}
}

func TestManagerStopAllEmpty(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	// Should not panic with no processes.
	mgr.StopAll()
}

func TestManagerLoadConfigExplicitGroups(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web":    defaultProgramConfig(),
			"worker": defaultProgramConfig(),
		},
		Groups: map[string]config.GroupConfig{
			"all": {Programs: []string{"web", "worker"}, Priority: 50},
		},
	})

	groups := mgr.Groups()
	if groups["all"] == nil {
		t.Fatal("explicit group 'all' not created")
	}
	if groups["all"].Priority != 50 {
		t.Fatalf("priority = %d, want 50", groups["all"].Priority)
	}
	if len(groups["all"].Processes) != 2 {
		t.Fatalf("expected 2 processes in 'all', got %d", len(groups["all"].Processes))
	}
}

func TestManagerProcessInfo(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	cfg := defaultProgramConfig()
	cfg.Description = "test process"
	mgr.LoadConfig(&config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": cfg,
		},
	})

	info, err := mgr.Get("web")
	if err != nil {
		t.Fatal(err)
	}
	if info.Group != "web" {
		t.Fatalf("group = %q, want web", info.Group)
	}
	if info.Description != "test process" {
		t.Fatalf("description = %q, want 'test process'", info.Description)
	}
	if info.PID != 0 {
		t.Fatalf("pid = %d, want 0 (not started)", info.PID)
	}
}
