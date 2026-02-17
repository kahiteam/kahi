package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
	"github.com/kahiteam/kahi/internal/logging"
	"github.com/kahiteam/kahi/internal/process"
	"github.com/kahiteam/kahi/internal/version"
)

// Supervisor is the main daemon run loop.
type Supervisor struct {
	mu         sync.Mutex
	config     *config.Config
	configPath string
	manager    *process.Manager
	bus        *events.Bus
	ticker     *events.Ticker
	signals    *SignalQueue
	logger     *slog.Logger
	captures   map[string]*logging.CaptureWriter
	shutting   bool
	shutdownCh chan struct{}
	doneCh     chan struct{}
	pidFile    string
}

// SupervisorConfig configures the supervisor.
type SupervisorConfig struct {
	Config     *config.Config
	ConfigPath string
	PIDFile    string
	Logger     *slog.Logger
}

// New creates a supervisor.
func New(cfg SupervisorConfig) *Supervisor {
	bus := events.NewBus(cfg.Logger)
	spawner := &process.ExecSpawner{}
	mgr := process.NewManager(spawner, bus, cfg.Logger)

	return &Supervisor{
		config:     cfg.Config,
		configPath: cfg.ConfigPath,
		manager:    mgr,
		bus:        bus,
		logger:     cfg.Logger,
		captures:   make(map[string]*logging.CaptureWriter),
		shutdownCh: make(chan struct{}),
		doneCh:     make(chan struct{}),
		pidFile:    cfg.PIDFile,
	}
}

// Manager returns the process manager.
func (s *Supervisor) Manager() *process.Manager { return s.manager }

// Bus returns the event bus.
func (s *Supervisor) Bus() *events.Bus { return s.bus }

// Run starts the supervisor main loop. Blocks until shutdown.
func (s *Supervisor) Run() error {
	// Write PID file.
	if err := WritePIDFile(s.pidFile); err != nil {
		return err
	}
	defer RemovePIDFile(s.pidFile)

	// Load processes from config.
	s.manager.LoadConfig(s.config)

	// Start periodic ticks.
	s.ticker = events.NewTicker(s.bus)
	defer s.ticker.Stop()

	// Start signal handler.
	s.signals = NewSignalQueue(s.logger)
	defer s.signals.Stop()

	// Publish supervisor running event.
	s.bus.Publish(events.Event{
		Type: events.SupervisorStateRunning,
		Data: map[string]string{},
	})

	// Autostart processes.
	s.manager.AutostartAll()

	s.logger.Info("supervisor running", "pid", os.Getpid())

	// Main event loop.
	for {
		select {
		case sig := <-s.signals.C:
			if s.handleSignal(sig) {
				goto shutdown
			}
		case <-s.shutdownCh:
			goto shutdown
		}
	}

shutdown:
	s.logger.Info("shutting down")

	// Publish supervisor stopping event.
	s.bus.Publish(events.Event{
		Type: events.SupervisorStateStopping,
		Data: map[string]string{},
	})

	// Stop all processes in reverse priority order.
	s.manager.StopAll()

	// Wait for processes to exit with timeout.
	s.waitForShutdown()

	// Close capture writers.
	for _, cw := range s.captures {
		cw.Close()
	}

	close(s.doneCh)
	s.logger.Info("shutdown complete")
	return nil
}

// handleSignal processes a signal and returns true if shutdown should begin.
func (s *Supervisor) handleSignal(sig os.Signal) bool {
	s.logger.Info("received signal", "signal", sig.String())

	switch sig {
	case syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT:
		return true

	case syscall.SIGHUP:
		s.handleReload()
		return false

	case syscall.SIGUSR2:
		s.handleLogReopen()
		return false

	case syscall.SIGCHLD:
		s.handleSigchld()
		return false

	default:
		s.logger.Warn("unhandled signal", "signal", sig.String())
		return false
	}
}

func (s *Supervisor) handleReload() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.shutting {
		s.logger.Warn("ignoring reload during shutdown")
		return
	}

	s.logger.Info("reloading config", "path", s.configPath)

	newCfg, warnings, err := config.LoadWithIncludes(s.configPath)
	if err != nil {
		s.logger.Error("reload failed", "error", err)
		return
	}
	for _, w := range warnings {
		s.logger.Warn("config warning", "warning", w)
	}

	added, changed, removed := process.ConfigDiff(s.config, newCfg)
	s.logger.Info("config diff", "added", added, "changed", changed, "removed", removed)

	// Remove deleted programs.
	for _, name := range removed {
		if err := s.manager.StopGroup(name); err != nil {
			s.logger.Error("stop group failed", "group", name, "error", err)
		}
		if err := s.manager.RemoveGroup(name); err != nil {
			s.logger.Error("remove group failed", "group", name, "error", err)
		}
	}

	// Update changed programs.
	for _, name := range changed {
		if err := s.manager.StopGroup(name); err != nil {
			s.logger.Error("stop changed group failed", "group", name, "error", err)
		}
	}

	// Apply new config.
	s.config = newCfg
	s.manager.LoadConfig(newCfg)

	// Start new/changed programs.
	for _, name := range added {
		if err := s.manager.StartGroup(name); err != nil {
			s.logger.Error("start new group failed", "group", name, "error", err)
		}
	}
	for _, name := range changed {
		if err := s.manager.StartGroup(name); err != nil {
			s.logger.Error("restart changed group failed", "group", name, "error", err)
		}
	}
}

func (s *Supervisor) handleLogReopen() {
	s.logger.Info("reopening log files")
	for name, cw := range s.captures {
		if err := cw.Reopen(); err != nil {
			s.logger.Error("log reopen failed", "process", name, "error", err)
		}
	}
}

func (s *Supervisor) handleSigchld() {
	// Reap all exited children in a loop to handle coalesced SIGCHLD.
	for {
		pid, status, err := reapChild()
		if err != nil || pid <= 0 {
			break
		}

		p := s.manager.ProcessByPid(pid)
		if p == nil {
			s.logger.Warn("reaped unknown child", "pid", pid, "status", status)
			continue
		}

		s.logger.Debug("reaped child", "pid", pid, "process", p.Name(), "status", status)
		p.HandleExit(status)
	}
}

// reapChild wraps waitpid with WNOHANG. Returns 0 when no more children.
// It is a var so tests can override the reaping behavior.
var reapChild = func() (int, int, error) {
	var ws syscall.WaitStatus
	pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
	if err != nil {
		return 0, 0, err
	}
	return pid, ws.ExitStatus(), nil
}

// ReapAllZombies reaps all zombie children when running as PID 1.
func ReapAllZombies(logger *slog.Logger) {
	for {
		pid, _, err := reapChild()
		if err != nil || pid <= 0 {
			break
		}
		logger.Debug("reaped zombie", "pid", pid)
	}
}

func (s *Supervisor) waitForShutdown() {
	timeout := time.Duration(s.config.Supervisor.ShutdownTimeout) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Warn("shutdown timeout exceeded, killing remaining processes")
			return
		case <-ticker.C:
			allStopped := true
			for _, p := range s.manager.Processes() {
				state := p.State()
				if state == process.Running || state == process.Starting || state == process.Stopping {
					allStopped = false
					break
				}
			}
			if allStopped {
				return
			}
		}
	}
}

// Shutdown triggers a graceful shutdown.
func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.shutting {
		s.shutting = true
		close(s.shutdownCh)
	}
}

// Done returns a channel that closes when the supervisor has finished.
func (s *Supervisor) Done() <-chan struct{} { return s.doneCh }

// IsShuttingDown returns true if the supervisor is shutting down.
func (s *Supervisor) IsShuttingDown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutting
}

// IsReady returns true if the supervisor is running and all autostart
// processes have reached RUNNING state.
func (s *Supervisor) IsReady() bool {
	for _, p := range s.manager.Processes() {
		cfg := p.Config()
		if cfg.Autostart != nil && *cfg.Autostart {
			if p.State() != process.Running {
				return false
			}
		}
	}
	return true
}

// CheckReady checks if specific processes are ready.
func (s *Supervisor) CheckReady(processes []string) (bool, []string, error) {
	var pending []string
	for _, name := range processes {
		p, err := s.manager.GetProcess(name)
		if err != nil {
			return false, nil, err
		}
		if p.State() != process.Running {
			pending = append(pending, name)
		}
	}
	return len(pending) == 0, pending, nil
}

// Version returns version info.
func (s *Supervisor) Version() map[string]string {
	return map[string]string{
		"version": version.Version,
		"commit":  version.Commit,
		"date":    version.Date,
		"pid":     fmt.Sprintf("%d", os.Getpid()),
	}
}

// PID returns the daemon PID.
func (s *Supervisor) PID() int { return os.Getpid() }

// GetConfig returns the current config.
func (s *Supervisor) GetConfig() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config
}

// Reload re-reads config and applies changes.
func (s *Supervisor) Reload() (added, changed, removed []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newCfg, _, err := config.LoadWithIncludes(s.configPath)
	if err != nil {
		return nil, nil, nil, err
	}

	added, changed, removed = process.ConfigDiff(s.config, newCfg)
	s.config = newCfg
	s.manager.LoadConfig(newCfg)
	return added, changed, removed, nil
}
