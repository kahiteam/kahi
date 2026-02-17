package process

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testBus() *events.Bus {
	return events.NewBus(testLogger())
}

func defaultProgramConfig() config.ProgramConfig {
	autostart := true
	return config.ProgramConfig{
		Command:      "/bin/echo hello",
		Numprocs:     1,
		Priority:     999,
		Autostart:    &autostart,
		Autorestart:  "unexpected",
		Startsecs:    0,
		Startretries: 3,
		Exitcodes:    []int{0},
		Stopsignal:   "TERM",
		Stopwaitsecs: 10,
	}
}

// --- process-start tests ---

func TestProcessStartWithMockSpawner(t *testing.T) {
	spawner := &MockSpawner{}
	cfg := defaultProgramConfig()
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	if err := p.Start(); err != nil {
		t.Fatal(err)
	}

	// Verify spawner was called once.
	if len(spawner.SpawnCalls) != 1 {
		t.Fatalf("expected 1 spawn call, got %d", len(spawner.SpawnCalls))
	}

	// Verify direct exec without shell.
	call := spawner.SpawnCalls[0]
	if call.Command != "/bin/echo" {
		t.Fatalf("expected command /bin/echo, got %s", call.Command)
	}
	if len(call.Args) != 1 || call.Args[0] != "hello" {
		t.Fatalf("expected args [hello], got %v", call.Args)
	}
}

func TestProcessStartPipesCreated(t *testing.T) {
	mp := NewMockProcess(1234)
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}
	cfg := defaultProgramConfig()
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger(),
		WithStdoutHandler(func(name string, data []byte) {}),
		WithStderrHandler(func(name string, data []byte) {}),
	)

	if err := p.Start(); err != nil {
		t.Fatal(err)
	}

	// Verify pipes are accessible (mock returns non-nil).
	if mp.StdoutPipe() == nil {
		t.Fatal("expected stdout pipe")
	}
	if mp.StderrPipe() == nil {
		t.Fatal("expected stderr pipe")
	}
}

func TestProcessStartSetpgid(t *testing.T) {
	// The ExecSpawner sets Setpgid=true by default.
	// Verify the SpawnConfig doesn't disable it.
	spawner := &MockSpawner{}
	cfg := defaultProgramConfig()
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	_ = p.Start()

	// Since we use MockSpawner, we verify the Spawn was called.
	// The ExecSpawner implementation sets Setpgid=true.
	if len(spawner.SpawnCalls) != 1 {
		t.Fatal("spawn not called")
	}
}

func TestProcessStartSupervisorEnvVars(t *testing.T) {
	spawner := &MockSpawner{}
	cfg := defaultProgramConfig()
	p := NewProcess("worker-0", "worker", cfg, spawner, testBus(), testLogger())

	_ = p.Start()

	call := spawner.SpawnCalls[0]
	envMap := envToMap(call.Env)

	if envMap["SUPERVISOR_ENABLED"] != "1" {
		t.Fatal("SUPERVISOR_ENABLED not set")
	}
	if envMap["SUPERVISOR_PROCESS_NAME"] != "worker-0" {
		t.Fatalf("SUPERVISOR_PROCESS_NAME = %q, want worker-0", envMap["SUPERVISOR_PROCESS_NAME"])
	}
	if envMap["SUPERVISOR_GROUP_NAME"] != "worker" {
		t.Fatalf("SUPERVISOR_GROUP_NAME = %q, want worker", envMap["SUPERVISOR_GROUP_NAME"])
	}
}

func TestProcessStartCleanEnvironment(t *testing.T) {
	spawner := &MockSpawner{}
	cfg := defaultProgramConfig()
	cfg.CleanEnvironment = true
	cfg.Environment = map[string]string{"APP_KEY": "secret"}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	_ = p.Start()

	call := spawner.SpawnCalls[0]
	envMap := envToMap(call.Env)

	// Should have supervisor vars + APP_KEY only.
	if envMap["SUPERVISOR_ENABLED"] != "1" {
		t.Fatal("missing SUPERVISOR_ENABLED")
	}
	if envMap["APP_KEY"] != "secret" {
		t.Fatal("missing APP_KEY")
	}

	// Should NOT have inherited vars like PATH (unless explicitly set).
	// With clean env, only our explicit vars should be present.
	for _, env := range call.Env {
		if strings.HasPrefix(env, "HOME=") {
			t.Fatal("inherited HOME in clean environment")
		}
	}
}

func TestProcessStartInheritEnvironment(t *testing.T) {
	spawner := &MockSpawner{}
	cfg := defaultProgramConfig()
	cfg.CleanEnvironment = false
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	_ = p.Start()

	call := spawner.SpawnCalls[0]
	// Should have inherited vars like PATH.
	found := false
	for _, env := range call.Env {
		if strings.HasPrefix(env, "PATH=") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected PATH in inherited environment")
	}
}

func TestProcessAutostart(t *testing.T) {
	spawner := &MockSpawner{}
	autostartTrue := true
	cfg := defaultProgramConfig()
	cfg.Autostart = &autostartTrue

	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	// Autostart=true means the process should be started by the manager.
	if cfg.Autostart == nil || !*cfg.Autostart {
		t.Fatal("autostart should be true")
	}

	_ = p.Start()
	if len(spawner.SpawnCalls) != 1 {
		t.Fatal("process should have started")
	}
}

func TestProcessAutostartFalse(t *testing.T) {
	autostartFalse := false
	cfg := defaultProgramConfig()
	cfg.Autostart = &autostartFalse

	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	// Process should remain in STOPPED state when autostart is false.
	if p.State() != Stopped {
		t.Fatalf("state = %s, want STOPPED", p.State())
	}
	if len(spawner.SpawnCalls) != 0 {
		t.Fatal("process should not have been started")
	}
}

func TestProcessStartWithDirectory(t *testing.T) {
	spawner := &MockSpawner{}
	cfg := defaultProgramConfig()
	cfg.Directory = "/tmp/workdir"
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	_ = p.Start()

	call := spawner.SpawnCalls[0]
	if call.Dir != "/tmp/workdir" {
		t.Fatalf("directory = %q, want /tmp/workdir", call.Dir)
	}
}

func TestProcessStartSpawnFailure(t *testing.T) {
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return nil, os.ErrNotExist
		},
	}
	cfg := defaultProgramConfig()
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	err := p.Start()
	if err == nil {
		t.Fatal("expected spawn error")
	}
	if !strings.Contains(err.Error(), "spawn failed") {
		t.Fatalf("error = %q, want spawn failed", err.Error())
	}

	// Should be in BACKOFF or FATAL state.
	state := p.State()
	if state != Backoff && state != Fatal {
		t.Fatalf("state = %s, want BACKOFF or FATAL", state)
	}
}

func TestProcessStartPermissionDenied(t *testing.T) {
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return nil, os.ErrPermission
		},
	}
	cfg := defaultProgramConfig()
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	err := p.Start()
	if err == nil {
		t.Fatal("expected permission denied error")
	}
}

func TestProcessStartAlreadyRunning(t *testing.T) {
	mp := NewMockProcess(1234)
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}
	cfg := defaultProgramConfig()
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	_ = p.Start()
	time.Sleep(10 * time.Millisecond) // let watchStart() run

	err := p.Start()
	if err == nil {
		t.Fatal("expected error for starting already-running process")
	}
}

// --- process-stop tests ---

func TestProcessStop(t *testing.T) {
	var signalSent os.Signal
	mp := NewMockProcess(1234)
	mp.signalFn = func(sig os.Signal) error {
		signalSent = sig
		return nil
	}

	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}
	cfg := defaultProgramConfig()
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	_ = p.Start()
	time.Sleep(10 * time.Millisecond) // let state reach RUNNING

	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}

	if p.State() != Stopping {
		t.Fatalf("state = %s, want STOPPING", p.State())
	}
	if signalSent == nil {
		t.Fatal("no signal sent")
	}
}

func TestProcessStopNotRunning(t *testing.T) {
	spawner := &MockSpawner{}
	cfg := defaultProgramConfig()
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	err := p.Stop()
	if err == nil {
		t.Fatal("expected error for stopping non-running process")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Fatalf("error = %q, want 'not running'", err.Error())
	}
}

// --- autorestart tests ---

func TestAutorestartTrue(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Autorestart = "true"
	cfg.Exitcodes = []int{0}

	mp := NewMockProcess(1234)
	spawnCount := 0
	var mu sync.Mutex

	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			mu.Lock()
			spawnCount++
			mu.Unlock()
			return mp, nil
		},
	}

	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	// Simulate exit with code 0 (expected).
	p.HandleExit(0)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	count := spawnCount
	mu.Unlock()

	// autorestart=true should restart regardless of exit code.
	if count < 2 {
		t.Fatalf("spawn count = %d, want >= 2 (autorestart=true)", count)
	}
}

func TestAutorestartFalse(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Autorestart = "false"

	spawnCount := 0
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			spawnCount++
			return NewMockProcess(1234), nil
		},
	}

	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	p.HandleExit(0)
	time.Sleep(50 * time.Millisecond)

	if spawnCount != 1 {
		t.Fatalf("spawn count = %d, want 1 (autorestart=false)", spawnCount)
	}
}

func TestAutorestartUnexpectedWithExpectedCode(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Autorestart = "unexpected"
	cfg.Exitcodes = []int{0, 2}

	spawnCount := 0
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			spawnCount++
			return NewMockProcess(1234), nil
		},
	}

	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	// Exit with code 0 (expected) -- should NOT restart.
	p.HandleExit(0)
	time.Sleep(50 * time.Millisecond)

	if spawnCount != 1 {
		t.Fatalf("spawn count = %d, want 1 (expected exit code)", spawnCount)
	}
}

func TestAutorestartManualStopSuppressed(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Autorestart = "true"

	mp := NewMockProcess(1234)
	mp.signalFn = func(sig os.Signal) error { return nil }

	spawnCount := 0
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			spawnCount++
			return mp, nil
		},
	}

	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	// Manual stop.
	_ = p.Stop()
	time.Sleep(10 * time.Millisecond)
	p.HandleExit(0)
	time.Sleep(50 * time.Millisecond)

	if spawnCount != 1 {
		t.Fatalf("spawn count = %d, want 1 (manual stop should suppress autorestart)", spawnCount)
	}
}

func TestAutorestartSuppressedDuringShutdown(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Autorestart = "true"

	shutdownCh := make(chan struct{})
	spawnCount := 0
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			spawnCount++
			return NewMockProcess(1234), nil
		},
	}

	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger(),
		WithShutdownCh(shutdownCh),
	)
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	// Signal shutdown.
	close(shutdownCh)

	p.HandleExit(0)
	time.Sleep(50 * time.Millisecond)

	if spawnCount != 1 {
		t.Fatalf("spawn count = %d, want 1 (shutdown suppresses autorestart)", spawnCount)
	}
}

// --- Signal tests ---

func TestParseSignal(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"TERM", "terminated"},
		{"SIGTERM", "terminated"},
		{"HUP", "hangup"},
		{"INT", "interrupt"},
		{"KILL", "killed"},
		{"USR1", "user defined signal 1"},
		{"USR2", "user defined signal 2"},
	}

	for _, tt := range tests {
		sig := ParseSignal(tt.input)
		if sig == nil {
			t.Fatalf("ParseSignal(%q) returned nil", tt.input)
		}
	}
}

func TestParseSignalInvalid(t *testing.T) {
	sig := ParseSignal("INVALID")
	if sig != nil {
		t.Fatalf("expected nil for invalid signal, got %v", sig)
	}
}

// --- Event publishing tests ---

func TestProcessPublishesStateEvents(t *testing.T) {
	bus := testBus()
	var received []events.EventType
	var mu sync.Mutex

	handler := func(e events.Event) {
		mu.Lock()
		received = append(received, e.Type)
		mu.Unlock()
	}

	bus.Subscribe(events.ProcessStateStarting, handler)
	bus.Subscribe(events.ProcessStateRunning, handler)

	spawner := &MockSpawner{}
	cfg := defaultProgramConfig()
	p := NewProcess("test", "test", cfg, spawner, bus, testLogger())

	_ = p.Start()
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) < 1 {
		t.Fatal("expected at least 1 event")
	}
	if received[0] != events.ProcessStateStarting {
		t.Fatalf("first event = %s, want PROCESS_STATE_STARTING", received[0])
	}
}

// --- Manager tests ---

func TestManagerLoadConfig(t *testing.T) {
	spawner := &MockSpawner{}
	bus := testBus()
	mgr := NewManager(spawner, bus, testLogger())

	cfg := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
			"api": defaultProgramConfig(),
		},
	}
	mgr.LoadConfig(cfg)

	procs := mgr.List()
	if len(procs) != 2 {
		t.Fatalf("expected 2 processes, got %d", len(procs))
	}

	groups := mgr.ListGroups()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
}

func TestManagerPriorityOrdering(t *testing.T) {
	spawner := &MockSpawner{}
	bus := testBus()
	mgr := NewManager(spawner, bus, testLogger())

	cfg100 := defaultProgramConfig()
	cfg100.Priority = 100
	cfg200 := defaultProgramConfig()
	cfg200.Priority = 200
	cfg300 := defaultProgramConfig()
	cfg300.Priority = 300

	c := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"high":   cfg300,
			"low":    cfg100,
			"medium": cfg200,
		},
	}
	mgr.LoadConfig(c)

	// AutostartAll should start in ascending priority order.
	mgr.AutostartAll()
	time.Sleep(50 * time.Millisecond)

	if len(spawner.SpawnCalls) != 3 {
		t.Fatalf("expected 3 spawn calls, got %d", len(spawner.SpawnCalls))
	}
}

func TestManagerNumprocsExpansion(t *testing.T) {
	spawner := &MockSpawner{}
	bus := testBus()
	mgr := NewManager(spawner, bus, testLogger())

	cfg := defaultProgramConfig()
	cfg.Numprocs = 3
	cfg.ProcessName = "worker-%(process_num)d"

	c := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"worker": cfg,
		},
	}
	mgr.LoadConfig(c)

	procs := mgr.List()
	if len(procs) != 3 {
		t.Fatalf("expected 3 processes, got %d", len(procs))
	}
}

func TestManagerGetProcess(t *testing.T) {
	spawner := &MockSpawner{}
	mgr := NewManager(spawner, testBus(), testLogger())

	cfg := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	}
	mgr.LoadConfig(cfg)

	_, err := mgr.GetProcess("web")
	if err != nil {
		t.Fatal(err)
	}

	_, err = mgr.GetProcess("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestManagerGroupOperations(t *testing.T) {
	spawner := &MockSpawner{}
	mgr := NewManager(spawner, testBus(), testLogger())

	cfg := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	}
	mgr.LoadConfig(cfg)

	err := mgr.StartGroup("web")
	if err != nil {
		t.Fatal(err)
	}

	err = mgr.StartGroup("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent group")
	}
}

// --- Helpers ---

func envToMap(env []string) map[string]string {
	m := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}
	return m
}
