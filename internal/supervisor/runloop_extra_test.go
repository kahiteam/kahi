package supervisor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
	"github.com/kahiteam/kahi/internal/logging"
	"github.com/kahiteam/kahi/internal/process"
)

func testSupervisorWithMock(cfg *config.Config, spawner *process.MockSpawner) *Supervisor {
	logger := discardLogger()
	bus := events.NewBus(logger)
	mgr := process.NewManager(spawner, bus, logger)

	return &Supervisor{
		config:     cfg,
		configPath: "/nonexistent/kahi.toml",
		manager:    mgr,
		bus:        bus,
		logger:     logger,
		captures:   make(map[string]*logging.CaptureWriter),
		shutdownCh: make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
}

func TestWaitForShutdownTimeout(t *testing.T) {
	autostart := true
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: 1},
		Programs: map[string]config.ProgramConfig{
			"sleeper": {
				Command:      "/bin/sleep 3600",
				Numprocs:     1,
				Priority:     999,
				Autostart:    &autostart,
				Autorestart:  "false",
				Startsecs:    0,
				Startretries: 0,
				Exitcodes:    []int{0},
				Stopsignal:   "TERM",
				Stopwaitsecs: 10,
			},
		},
	}

	spawner := &process.MockSpawner{}
	s := testSupervisorWithMock(cfg, spawner)
	s.manager.LoadConfig(cfg)

	for _, p := range s.manager.Processes() {
		if err := p.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
	}

	start := time.Now()
	s.waitForShutdown()
	elapsed := time.Since(start)

	if elapsed < 900*time.Millisecond {
		t.Fatalf("waitForShutdown returned too early: %v", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("waitForShutdown took too long: %v", elapsed)
	}
}

func TestWaitForShutdownWithStoppedProcesses(t *testing.T) {
	autostart := false
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: 5},
		Programs: map[string]config.ProgramConfig{
			"worker": {
				Command:      "/bin/echo",
				Numprocs:     1,
				Priority:     999,
				Autostart:    &autostart,
				Autorestart:  "false",
				Stopsignal:   "TERM",
				Stopwaitsecs: 10,
				Exitcodes:    []int{0},
			},
		},
	}

	spawner := &process.MockSpawner{}
	s := testSupervisorWithMock(cfg, spawner)
	s.manager.LoadConfig(cfg)

	start := time.Now()
	s.waitForShutdown()
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("waitForShutdown with stopped processes took %v", elapsed)
	}
}

func TestHandleReloadDuringShutdown(t *testing.T) {
	s := testSupervisor()
	s.shutting = true
	s.handleReload()

	if _, ok := s.config.Programs["web"]; !ok {
		t.Fatal("config should not change during shutdown reload")
	}
}

func TestHandleReloadConfigLoadError(t *testing.T) {
	s := testSupervisor()
	s.handleReload()

	if _, ok := s.config.Programs["web"]; !ok {
		t.Fatal("config should not change after failed reload")
	}
}

func TestHandleSignalSIGHUP(t *testing.T) {
	s := testSupervisor()
	s.manager.LoadConfig(s.config)

	if s.handleSignal(syscall.SIGHUP) {
		t.Fatal("SIGHUP should not trigger shutdown")
	}
}

func TestReloadErrorPath(t *testing.T) {
	s := testSupervisor()
	added, changed, removed, err := s.Reload()
	if err == nil {
		t.Fatal("expected error for nonexistent config")
	}
	if added != nil || changed != nil || removed != nil {
		t.Fatal("expected nil slices on error")
	}
}

func TestReloadSuccessPath(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "kahi.toml")

	content := `[supervisor]
shutdown_timeout = 5

[programs.web]
command = "/bin/echo hello"
numprocs = 1
priority = 999
autorestart = "false"
stopsignal = "TERM"
stopwaitsecs = 10
exitcodes = [0]
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Load initial config from the same file so both sides of ConfigDiff match.
	cfg, _, err := config.LoadWithIncludes(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	s := New(SupervisorConfig{
		Config:     cfg,
		ConfigPath: cfgPath,
		Logger:     discardLogger(),
	})

	added, changed, removed, err := s.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 || len(changed) != 0 || len(removed) != 0 {
		t.Fatalf("expected no diff, got added=%v changed=%v removed=%v", added, changed, removed)
	}
}

func TestReapAllZombies(t *testing.T) {
	ReapAllZombies(discardLogger())
}

func TestRunPIDFileError(t *testing.T) {
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: 1},
		Programs:   map[string]config.ProgramConfig{},
	}

	s := New(SupervisorConfig{
		Config:  cfg,
		PIDFile: "/nonexistent/dir/kahi.pid",
		Logger:  discardLogger(),
	})

	err := s.Run()
	if err == nil {
		t.Fatal("expected error for invalid PID file path")
	}
}

func TestHandleLogReopenWithCaptures(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	cw, err := logging.NewCaptureWriter(logging.CaptureConfig{
		ProcessName: "test-proc",
		Stream:      "stdout",
		Logfile:     logPath,
		Logger:      discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cw.Close()

	_, _ = cw.Write([]byte("before\n"))

	s := testSupervisor()
	s.captures["test-proc:stdout"] = cw

	s.handleLogReopen()

	_, _ = cw.Write([]byte("after\n"))

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty log after reopen")
	}
}

func TestHandleLogReopenWithBadCapture(t *testing.T) {
	tmpDir := t.TempDir()
	subdir := filepath.Join(tmpDir, "subdir")
	_ = os.MkdirAll(subdir, 0755)
	logPath := filepath.Join(subdir, "test.log")

	cw, err := logging.NewCaptureWriter(logging.CaptureConfig{
		ProcessName: "bad-proc",
		Stream:      "stdout",
		Logfile:     logPath,
		Logger:      discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cw.Close()

	os.RemoveAll(subdir)

	s := testSupervisor()
	s.captures["bad-proc:stdout"] = cw
	s.handleLogReopen()
}

func TestRunClosesCaptures(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "capture.log")

	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: 1},
		Programs:   map[string]config.ProgramConfig{},
	}

	s := New(SupervisorConfig{
		Config: cfg,
		Logger: discardLogger(),
	})

	cw, err := logging.NewCaptureWriter(logging.CaptureConfig{
		ProcessName: "cap-test",
		Stream:      "stdout",
		Logfile:     logPath,
		Logger:      discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.captures["cap-test:stdout"] = cw

	go func() {
		time.Sleep(50 * time.Millisecond)
		s.Shutdown()
	}()

	if err := s.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestHandleReloadWithValidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "kahi.toml")

	content := `[supervisor]
shutdown_timeout = 5

[programs.api]
command = "/bin/api"
numprocs = 1
priority = 100
autorestart = "false"
stopsignal = "TERM"
stopwaitsecs = 10
exitcodes = [0]
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	autostart := true
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: 5},
		Programs: map[string]config.ProgramConfig{
			"web": {
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
			},
		},
	}

	spawner := &process.MockSpawner{}
	logger := discardLogger()
	bus := events.NewBus(logger)
	mgr := process.NewManager(spawner, bus, logger)

	s := &Supervisor{
		config:     cfg,
		configPath: cfgPath,
		manager:    mgr,
		bus:        bus,
		logger:     logger,
		captures:   make(map[string]*logging.CaptureWriter),
		shutdownCh: make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	s.manager.LoadConfig(cfg)

	s.handleReload()

	if _, ok := s.config.Programs["api"]; !ok {
		t.Fatal("expected 'api' program after reload")
	}
}

func TestHandleReloadChangedPrograms(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "kahi.toml")

	// New config has "web" with a different command (changed) and "api" (added).
	content := `[supervisor]
shutdown_timeout = 5

[programs.web]
command = "/bin/echo goodbye"
numprocs = 1
priority = 999
autorestart = "false"
stopsignal = "TERM"
stopwaitsecs = 10
exitcodes = [0]

[programs.api]
command = "/bin/api"
numprocs = 1
priority = 100
autorestart = "false"
stopsignal = "TERM"
stopwaitsecs = 10
exitcodes = [0]
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	autostart := true
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: 5},
		Programs: map[string]config.ProgramConfig{
			"web": {
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
			},
			"worker": {
				Command:      "/bin/worker",
				Numprocs:     1,
				Priority:     500,
				Autostart:    &autostart,
				Autorestart:  "false",
				Stopsignal:   "TERM",
				Stopwaitsecs: 10,
				Exitcodes:    []int{0},
			},
		},
	}

	spawner := &process.MockSpawner{}
	logger := discardLogger()
	bus := events.NewBus(logger)
	mgr := process.NewManager(spawner, bus, logger)

	s := &Supervisor{
		config:     cfg,
		configPath: cfgPath,
		manager:    mgr,
		bus:        bus,
		logger:     logger,
		captures:   make(map[string]*logging.CaptureWriter),
		shutdownCh: make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	s.manager.LoadConfig(cfg)

	s.handleReload()

	// "web" should be changed (different command).
	if _, ok := s.config.Programs["web"]; !ok {
		t.Fatal("expected 'web' program after reload")
	}
	// "api" should be added.
	if _, ok := s.config.Programs["api"]; !ok {
		t.Fatal("expected 'api' program after reload")
	}
	// "worker" should be removed.
	if _, ok := s.config.Programs["worker"]; ok {
		t.Fatal("'worker' should have been removed after reload")
	}
}

func TestHandleSigchldUnknownChild(t *testing.T) {
	original := reapChild
	callCount := 0
	reapChild = func() (int, int, error) {
		callCount++
		if callCount == 1 {
			return 99999, 0, nil // Unknown PID.
		}
		return 0, 0, nil // No more children.
	}
	defer func() { reapChild = original }()

	s := testSupervisor()
	s.manager.LoadConfig(s.config)
	s.handleSigchld()

	if callCount < 2 {
		t.Fatalf("expected reapChild called at least twice, got %d", callCount)
	}
}

func TestHandleSigchldKnownChild(t *testing.T) {
	autostart := true
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: 5},
		Programs: map[string]config.ProgramConfig{
			"web": {
				Command:       "/bin/echo hello",
				NumprocsStart: 0,
				Numprocs:      1,
				Priority:      999,
				Autostart:     &autostart,
				Autorestart:   "false",
				Startsecs:     0,
				Startretries:  0,
				Exitcodes:     []int{0},
				Stopsignal:    "TERM",
				Stopwaitsecs:  10,
			},
		},
	}

	spawner := &process.MockSpawner{}
	s := testSupervisorWithMock(cfg, spawner)
	s.manager.LoadConfig(cfg)

	// Start the process so it has a PID.
	procs := s.manager.Processes()
	if len(procs) == 0 {
		t.Fatal("expected at least one process")
	}
	if err := procs[0].Start(); err != nil {
		t.Fatal(err)
	}
	pid := procs[0].Pid()

	original := reapChild
	callCount := 0
	reapChild = func() (int, int, error) {
		callCount++
		if callCount == 1 {
			return pid, 0, nil
		}
		return 0, 0, nil
	}
	defer func() { reapChild = original }()

	s.handleSigchld()

	if callCount < 2 {
		t.Fatalf("expected reapChild called at least twice, got %d", callCount)
	}
}

func TestReapAllZombiesMultiple(t *testing.T) {
	original := reapChild
	callCount := 0
	reapChild = func() (int, int, error) {
		callCount++
		if callCount <= 3 {
			return 100 + callCount, 0, nil
		}
		return 0, 0, nil
	}
	defer func() { reapChild = original }()

	ReapAllZombies(discardLogger())

	if callCount != 4 {
		t.Fatalf("expected 4 reapChild calls, got %d", callCount)
	}
}

func TestHandleReloadWithWarnings(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "kahi.toml")

	// Config with an include pattern that matches nothing, generating a warning.
	content := `[supervisor]
shutdown_timeout = 5

include = ["/nonexistent/path/*.toml"]

[programs.web]
command = "/bin/echo hello"
numprocs = 1
priority = 999
autorestart = "false"
stopsignal = "TERM"
stopwaitsecs = 10
exitcodes = [0]
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := config.LoadWithIncludes(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	spawner := &process.MockSpawner{}
	logger := discardLogger()
	bus := events.NewBus(logger)
	mgr := process.NewManager(spawner, bus, logger)

	s := &Supervisor{
		config:     cfg,
		configPath: cfgPath,
		manager:    mgr,
		bus:        bus,
		logger:     logger,
		captures:   make(map[string]*logging.CaptureWriter),
		shutdownCh: make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	s.manager.LoadConfig(cfg)
	s.handleReload()
}

func TestHandleReloadErrorBranches(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "kahi.toml")

	// New config is empty (all old programs removed).
	content := `[supervisor]
shutdown_timeout = 5
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	autostart := true
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: 5},
		Programs: map[string]config.ProgramConfig{
			"web": {
				Command:      "/bin/echo hello",
				Numprocs:     1,
				Priority:     999,
				Autostart:    &autostart,
				Autorestart:  "false",
				Stopsignal:   "TERM",
				Stopwaitsecs: 10,
				Exitcodes:    []int{0},
			},
		},
	}

	spawner := &process.MockSpawner{}
	logger := discardLogger()
	bus := events.NewBus(logger)
	mgr := process.NewManager(spawner, bus, logger)

	s := &Supervisor{
		config:     cfg,
		configPath: cfgPath,
		manager:    mgr,
		bus:        bus,
		logger:     logger,
		captures:   make(map[string]*logging.CaptureWriter),
		shutdownCh: make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	// Deliberately do NOT call s.manager.LoadConfig(cfg) so that StopGroup/RemoveGroup
	// fail on the "removed" programs, exercising the error branches.
	s.handleReload()

	// After reload, the config should have been updated (empty programs).
	if _, ok := s.config.Programs["web"]; ok {
		t.Fatal("'web' should have been removed from config")
	}
}
