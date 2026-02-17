package process

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
)

func TestHandleExitFromStarting(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Startsecs = 10 // long startsecs so process stays in Starting
	cfg.Startretries = 1

	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()

	// Process is in Starting state; HandleExit should transition to Backoff.
	p.HandleExit(0)
	time.Sleep(10 * time.Millisecond)

	state := p.State()
	if state != Backoff && state != Fatal {
		t.Fatalf("state = %s, want BACKOFF or FATAL", state)
	}
}

func TestHandleExitFromStopping(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Autorestart = "true"

	mp := NewMockProcess(1234)
	mp.signalFn = func(sig os.Signal) error { return nil }

	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}

	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	_ = p.Stop()
	time.Sleep(10 * time.Millisecond)

	spawnCount := len(spawner.SpawnCalls)

	// HandleExit from Stopping state should go to Stopped, not restart.
	p.HandleExit(0)
	time.Sleep(50 * time.Millisecond)

	if p.State() != Stopped {
		t.Fatalf("state = %s, want STOPPED", p.State())
	}
	if len(spawner.SpawnCalls) != spawnCount {
		t.Fatal("should not restart after manual stop")
	}
}

func TestUptimeZeroWhenNotStarted(t *testing.T) {
	cfg := defaultProgramConfig()
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	if p.Uptime() != 0 {
		t.Fatalf("Uptime = %d, want 0 for not-started process", p.Uptime())
	}
}

func TestUptimePositiveWhenRunning(t *testing.T) {
	cfg := defaultProgramConfig()
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(50 * time.Millisecond)

	up := p.Uptime()
	if up < 0 {
		t.Fatalf("Uptime = %d, want >= 0", up)
	}
}

func TestUptimeZeroAfterExit(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Autorestart = "false"
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	p.HandleExit(0)
	time.Sleep(10 * time.Millisecond)

	if p.Uptime() != 0 {
		t.Fatalf("Uptime = %d, want 0 after exit", p.Uptime())
	}
}

func TestReadPipe(t *testing.T) {
	cfg := defaultProgramConfig()
	var received []byte
	var mu sync.Mutex

	mp := NewMockProcess(1234)
	pr, pw := io.Pipe()
	mp.stdout = pr

	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}

	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger(),
		WithStdoutHandler(func(name string, data []byte) {
			mu.Lock()
			received = append(received, data...)
			mu.Unlock()
		}),
	)

	_ = p.Start()
	_, _ = pw.Write([]byte("hello"))
	pw.Close()
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(string(received), "hello") {
		t.Fatalf("received = %q, want 'hello'", received)
	}
}

func TestShouldRestartUnexpectedWithNonMatchingCode(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Autorestart = "unexpected"
	cfg.Exitcodes = []int{2} // only exit code 2 is "expected"

	var spawnCount int
	var mu sync.Mutex
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			mu.Lock()
			spawnCount++
			mu.Unlock()
			return NewMockProcess(1234), nil
		},
	}

	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	// HandleExit(0) yields exit code 0, which is not in exitcodes [2],
	// so autorestart="unexpected" should trigger a restart.
	p.HandleExit(0)
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	count := spawnCount
	mu.Unlock()

	if count < 2 {
		t.Fatalf("spawn count = %d, want >= 2 (unexpected exit should restart)", count)
	}
}

func TestParseCommandEmpty(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Command = ""
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	cmd, args := p.parseCommand()
	if cmd != "" {
		t.Fatalf("cmd = %q, want empty", cmd)
	}
	if args != nil {
		t.Fatalf("args = %v, want nil", args)
	}
}

func TestParseSignalAllVariants(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"QUIT", true},
		{"SIGQUIT", true},
		{"STOP", true},
		{"CONT", true},
		{"quit", true},
		{"sigkill", true},
		{"INVALID", false},
		{"BOGUS", false},
	}
	for _, tt := range tests {
		sig := ParseSignal(tt.input)
		got := sig != nil
		if got != tt.want {
			t.Errorf("ParseSignal(%q) nil=%v, want nil=%v", tt.input, !got, !tt.want)
		}
	}
}

func TestDoPublishStateNilBus(t *testing.T) {
	cfg := defaultProgramConfig()
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, nil, testLogger())

	// Should not panic with nil bus.
	p.doPublishState(Running, 1234)
}

func TestDoPublishStateAllMappings(t *testing.T) {
	bus := testBus()
	var received []events.EventType
	var mu sync.Mutex

	handler := func(e events.Event) {
		mu.Lock()
		received = append(received, e.Type)
		mu.Unlock()
	}

	bus.Subscribe(events.ProcessStateStopped, handler)
	bus.Subscribe(events.ProcessStateStarting, handler)
	bus.Subscribe(events.ProcessStateRunning, handler)
	bus.Subscribe(events.ProcessStateBackoff, handler)
	bus.Subscribe(events.ProcessStateStopping, handler)
	bus.Subscribe(events.ProcessStateExited, handler)
	bus.Subscribe(events.ProcessStateFatal, handler)

	cfg := defaultProgramConfig()
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, bus, testLogger())

	states := []State{Stopped, Starting, Running, Backoff, Stopping, Exited, Fatal}
	for _, s := range states {
		p.doPublishState(s, 0)
	}

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()

	if len(received) != 7 {
		t.Fatalf("received %d events, want 7", len(received))
	}
}

func TestProcessSignalNotRunning(t *testing.T) {
	cfg := defaultProgramConfig()
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	err := p.Signal("TERM")
	if err == nil {
		t.Fatal("expected error for signal on non-running process")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Fatalf("error = %q, want 'not running'", err)
	}
}

func TestProcessSignalInvalid(t *testing.T) {
	mp := NewMockProcess(1234)
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}
	cfg := defaultProgramConfig()
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	err := p.Signal("INVALID")
	if err == nil {
		t.Fatal("expected error for invalid signal")
	}
	if !strings.Contains(err.Error(), "invalid signal") {
		t.Fatalf("error = %q, want 'invalid signal'", err)
	}
}

func TestProcessWriteStdinNotRunning(t *testing.T) {
	cfg := defaultProgramConfig()
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	err := p.WriteStdin([]byte("hello"))
	if err == nil {
		t.Fatal("expected error for stdin on non-running process")
	}
}

func TestManagerStopByName(t *testing.T) {
	mp := NewMockProcess(1234)
	mp.signalFn = func(sig os.Signal) error { return nil }

	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}
	mgr := NewManager(spawner, testBus(), testLogger())

	c := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	}
	mgr.LoadConfig(c)
	mgr.AutostartAll()
	time.Sleep(50 * time.Millisecond)

	err := mgr.Stop("web")
	if err != nil {
		t.Fatal(err)
	}
}

func TestManagerStopNotFound(t *testing.T) {
	spawner := &MockSpawner{}
	mgr := NewManager(spawner, testBus(), testLogger())

	err := mgr.Stop("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestManagerSignal(t *testing.T) {
	mp := NewMockProcess(1234)
	mp.signalFn = func(sig os.Signal) error { return nil }

	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}
	mgr := NewManager(spawner, testBus(), testLogger())

	c := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	}
	mgr.LoadConfig(c)
	mgr.AutostartAll()
	time.Sleep(50 * time.Millisecond)

	err := mgr.Signal("web", "USR1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestManagerWriteStdin(t *testing.T) {
	mp := NewMockProcess(1234)
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}
	mgr := NewManager(spawner, testBus(), testLogger())

	c := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	}
	mgr.LoadConfig(c)
	mgr.AutostartAll()
	time.Sleep(50 * time.Millisecond)

	err := mgr.WriteStdin("web", []byte("input"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestManagerRestartRunning(t *testing.T) {
	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			mp := NewMockProcess(1234)
			mp.signalFn = func(sig os.Signal) error { return nil }
			return mp, nil
		},
	}
	mgr := NewManager(spawner, testBus(), testLogger())

	c := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": defaultProgramConfig(),
		},
	}
	mgr.LoadConfig(c)
	mgr.AutostartAll()
	time.Sleep(50 * time.Millisecond)

	// Stop and simulate reap (HandleExit) to transition to Stopped.
	procs := mgr.Processes()
	if len(procs) == 0 {
		t.Fatal("no processes")
	}
	_ = procs[0].Stop()
	procs[0].HandleExit(0) // Simulate supervisor reap.
	time.Sleep(10 * time.Millisecond)

	// Now restart from stopped state.
	err := mgr.Restart("web")
	if err != nil {
		t.Fatal(err)
	}
}

func TestProcessStopasgroup(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Stopasgroup = true
	cfg.Killasgroup = true

	mp := NewMockProcess(1234)
	mp.signalFn = func(sig os.Signal) error { return nil }

	spawner := &MockSpawner{
		SpawnFn: func(cfg SpawnConfig) (SpawnedProcess, error) {
			return mp, nil
		},
	}

	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	err := p.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if p.State() != Stopping {
		t.Fatalf("state = %s, want STOPPING", p.State())
	}
}

func TestProcessStartedAt(t *testing.T) {
	cfg := defaultProgramConfig()
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	if !p.StartedAt().IsZero() {
		t.Fatal("StartedAt should be zero before start")
	}

	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	if p.StartedAt().IsZero() {
		t.Fatal("StartedAt should be non-zero after start")
	}
}

func TestProcessExitCode(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Autorestart = "false"
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	p.HandleExit(0)

	if p.ExitCode() != 0 {
		t.Fatalf("ExitCode = %d, want 0", p.ExitCode())
	}
}

func TestWithShutdownCh(t *testing.T) {
	ch := make(chan struct{})
	cfg := defaultProgramConfig()
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger(),
		WithShutdownCh(ch),
	)

	if p.shutdownCh != ch {
		t.Fatal("shutdownCh not set by option")
	}
}

func TestRestartAfterExitSpawnFailure(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Autorestart = "true"

	callCount := 0
	spawner := &MockSpawner{
		SpawnFn: func(c SpawnConfig) (SpawnedProcess, error) {
			callCount++
			if callCount == 1 {
				return NewMockProcess(1234), nil
			}
			return nil, os.ErrNotExist
		},
	}

	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	_ = p.Start()
	time.Sleep(10 * time.Millisecond)

	p.HandleExit(0)
	time.Sleep(100 * time.Millisecond)

	state := p.State()
	if state != Backoff && state != Fatal {
		t.Fatalf("state = %s, want BACKOFF or FATAL after spawn failure on restart", state)
	}
}
