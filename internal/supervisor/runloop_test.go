package supervisor

import (
	"io"
	"log/slog"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
	"github.com/kahiteam/kahi/internal/process"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testSupervisor() *Supervisor {
	autostart := true
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{
			ShutdownTimeout: 5,
		},
		Programs: map[string]config.ProgramConfig{
			"web": {
				Command:       "/bin/echo hello",
				NumprocsStart: 0,
				Numprocs:      1,
				Priority:      999,
				Autostart:     &autostart,
				Autorestart:   "unexpected",
				Startsecs:     0,
				Startretries:  3,
				Exitcodes:     []int{0},
				Stopsignal:    "TERM",
				Stopwaitsecs:  10,
			},
		},
	}

	return New(SupervisorConfig{
		Config:     cfg,
		ConfigPath: "/nonexistent/kahi.toml",
		Logger:     discardLogger(),
	})
}

func TestNewSupervisor(t *testing.T) {
	s := testSupervisor()
	if s == nil {
		t.Fatal("expected non-nil supervisor")
	}
	if s.config == nil {
		t.Fatal("expected non-nil config")
	}
	if s.manager == nil {
		t.Fatal("expected non-nil manager")
	}
	if s.bus == nil {
		t.Fatal("expected non-nil bus")
	}
	if s.captures == nil {
		t.Fatal("expected non-nil captures map")
	}
	if s.shutdownCh == nil {
		t.Fatal("expected non-nil shutdownCh")
	}
	if s.doneCh == nil {
		t.Fatal("expected non-nil doneCh")
	}
}

func TestSupervisorManager(t *testing.T) {
	s := testSupervisor()
	mgr := s.Manager()
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
}

func TestSupervisorBus(t *testing.T) {
	s := testSupervisor()
	bus := s.Bus()
	if bus == nil {
		t.Fatal("expected non-nil bus")
	}
}

func TestSupervisorPID(t *testing.T) {
	s := testSupervisor()
	pid := s.PID()
	if pid != os.Getpid() {
		t.Fatalf("PID() = %d, want %d", pid, os.Getpid())
	}
}

func TestSupervisorVersion(t *testing.T) {
	s := testSupervisor()
	v := s.Version()
	if v == nil {
		t.Fatal("expected non-nil version map")
	}
	if _, ok := v["version"]; !ok {
		t.Fatal("expected 'version' key in version map")
	}
}

func TestSupervisorDone(t *testing.T) {
	s := testSupervisor()
	ch := s.Done()
	if ch == nil {
		t.Fatal("expected non-nil done channel")
	}
	// Channel should not be closed yet.
	select {
	case <-ch:
		t.Fatal("done channel should not be closed before shutdown")
	default:
	}
}

func TestSupervisorShutdown(t *testing.T) {
	s := testSupervisor()

	if s.IsShuttingDown() {
		t.Fatal("should not be shutting down initially")
	}

	s.Shutdown()

	if !s.IsShuttingDown() {
		t.Fatal("should be shutting down after Shutdown()")
	}
}

func TestSupervisorShutdownIdempotent(t *testing.T) {
	s := testSupervisor()
	s.Shutdown()
	// Second call should not panic.
	s.Shutdown()

	if !s.IsShuttingDown() {
		t.Fatal("should remain shutting down")
	}
}

func TestSupervisorGetConfig(t *testing.T) {
	s := testSupervisor()
	cfg := s.GetConfig()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	c, ok := cfg.(*config.Config)
	if !ok {
		t.Fatal("expected config to be *config.Config")
	}
	if c.Supervisor.ShutdownTimeout != 5 {
		t.Fatalf("ShutdownTimeout = %d, want 5", c.Supervisor.ShutdownTimeout)
	}
}

func TestSupervisorIsReady(t *testing.T) {
	s := testSupervisor()
	// Load config to create processes.
	s.manager.LoadConfig(s.config)

	// Processes start in STOPPED state, not RUNNING. So with autostart=true
	// processes, IsReady should be false until they reach RUNNING.
	if s.IsReady() {
		t.Fatal("should not be ready before processes start")
	}
}

func TestSupervisorIsReadyNoAutostart(t *testing.T) {
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

	s := New(SupervisorConfig{Config: cfg, Logger: discardLogger()})
	s.manager.LoadConfig(cfg)

	// No autostart processes means IsReady should be true.
	if !s.IsReady() {
		t.Fatal("should be ready when no autostart processes exist")
	}
}

func TestSupervisorCheckReadyMissing(t *testing.T) {
	s := testSupervisor()
	s.manager.LoadConfig(s.config)

	_, _, err := s.CheckReady([]string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent process")
	}
}

func TestSupervisorCheckReadyPending(t *testing.T) {
	s := testSupervisor()
	s.manager.LoadConfig(s.config)

	ready, pending, err := s.CheckReady([]string{"web"})
	if err != nil {
		t.Fatal(err)
	}
	// web is in STOPPED state.
	if ready {
		t.Fatal("should not be ready when process is stopped")
	}
	if len(pending) != 1 || pending[0] != "web" {
		t.Fatalf("pending = %v, want [web]", pending)
	}
}

func TestHandleSignalTerminates(t *testing.T) {
	var s *Supervisor

	tests := []os.Signal{
		syscall.SIGTERM,
		syscall.SIGINT,
		syscall.SIGQUIT,
	}

	for _, sig := range tests {
		// Create fresh supervisor for each test.
		s = testSupervisor()
		if !s.handleSignal(sig) {
			t.Fatalf("handleSignal(%v) = false, want true (should trigger shutdown)", sig)
		}
	}
}

func TestHandleSignalNonTerminal(t *testing.T) {
	s := testSupervisor()
	s.manager.LoadConfig(s.config)

	// SIGUSR2 should not trigger shutdown.
	if s.handleSignal(syscall.SIGUSR2) {
		t.Fatal("SIGUSR2 should not trigger shutdown")
	}

	// SIGCHLD should not trigger shutdown.
	if s.handleSignal(syscall.SIGCHLD) {
		t.Fatal("SIGCHLD should not trigger shutdown")
	}
}

func TestHandleSignalUnknown(t *testing.T) {
	s := testSupervisor()
	// An unknown signal should return false.
	if s.handleSignal(syscall.SIGPIPE) {
		t.Fatal("unknown signal should not trigger shutdown")
	}
}

func TestHandleLogReopen(t *testing.T) {
	s := testSupervisor()
	// With no captures, this should be a no-op.
	s.handleLogReopen()
}

func TestHandleSigchld(t *testing.T) {
	s := testSupervisor()
	s.manager.LoadConfig(s.config)
	// handleSigchld should not panic with no children.
	s.handleSigchld()
}

func TestRunShutdownImmediately(t *testing.T) {
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: 1},
		Programs:   map[string]config.ProgramConfig{},
	}

	s := New(SupervisorConfig{
		Config: cfg,
		Logger: discardLogger(),
	})

	// Trigger shutdown from another goroutine.
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.Shutdown()
	}()

	err := s.Run()
	if err != nil {
		t.Fatal(err)
	}

	// Done channel should be closed.
	select {
	case <-s.Done():
	default:
		t.Fatal("done channel should be closed after Run returns")
	}
}

func TestRunPublishesEvents(t *testing.T) {
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: 1},
		Programs:   map[string]config.ProgramConfig{},
	}

	s := New(SupervisorConfig{
		Config: cfg,
		Logger: discardLogger(),
	})

	var receivedRunning, receivedStopping bool
	s.bus.Subscribe(events.SupervisorStateRunning, func(e events.Event) {
		receivedRunning = true
	})
	s.bus.Subscribe(events.SupervisorStateStopping, func(e events.Event) {
		receivedStopping = true
	})

	go func() {
		time.Sleep(50 * time.Millisecond)
		s.Shutdown()
	}()

	_ = s.Run()

	if !receivedRunning {
		t.Fatal("expected SupervisorStateRunning event")
	}
	if !receivedStopping {
		t.Fatal("expected SupervisorStateStopping event")
	}
}

func TestWaitForShutdownAllStopped(t *testing.T) {
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: 1},
		Programs:   map[string]config.ProgramConfig{},
	}

	s := New(SupervisorConfig{
		Config: cfg,
		Logger: discardLogger(),
	})
	s.manager.LoadConfig(cfg)

	// No processes means all are "stopped" immediately.
	start := time.Now()
	s.waitForShutdown()
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("waitForShutdown took %v, expected near-instant", elapsed)
	}
}

func TestConfigDiffBasic(t *testing.T) {
	old := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"web":    {Command: "/bin/web"},
			"worker": {Command: "/bin/worker"},
		},
	}
	new := &config.Config{
		Programs: map[string]config.ProgramConfig{
			"web": {Command: "/bin/web-v2"},
			"api": {Command: "/bin/api"},
		},
	}

	added, changed, removed := process.ConfigDiff(old, new)

	if len(added) != 1 || added[0] != "api" {
		t.Fatalf("added = %v, want [api]", added)
	}
	if len(changed) != 1 || changed[0] != "web" {
		t.Fatalf("changed = %v, want [web]", changed)
	}
	if len(removed) != 1 || removed[0] != "worker" {
		t.Fatalf("removed = %v, want [worker]", removed)
	}
}
