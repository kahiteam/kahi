package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
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
	shutting   bool
	shutdownCh chan struct{}
	doneCh     chan struct{}
	pidFile    string

	// reopenDaemonLog reopens the daemon's own log file; nil when logging
	// to stdout.
	reopenDaemonLog func() error

	// reloadMu serializes reloads. A reload waits for changed programs to
	// stop, so it never holds mu, which /healthz and shutdown need.
	reloadMu sync.Mutex

	// startupWeb is the [server.web] the daemon started with. The UI is
	// built once at startup, so reloads compare against this value.
	startupWeb config.WebServerConfig
}

// SupervisorConfig configures the supervisor.
type SupervisorConfig struct {
	Config     *config.Config
	ConfigPath string
	PIDFile    string
	Logger     *slog.Logger
	// ReopenDaemonLog reopens the daemon's own log file on SIGUSR2; nil when
	// the daemon logs to stdout.
	ReopenDaemonLog func() error
}

// New creates a supervisor.
func New(cfg SupervisorConfig) *Supervisor {
	bus := events.NewBus(cfg.Logger)
	spawner := &process.ExecSpawner{}
	mgr := process.NewManager(spawner, bus, cfg.Logger)

	var startupWeb config.WebServerConfig
	if cfg.Config != nil {
		startupWeb = cfg.Config.Server.Web
	}

	return &Supervisor{
		startupWeb: startupWeb,
		config:     cfg.Config,
		configPath: cfg.ConfigPath,
		manager:    mgr,
		bus:        bus,
		logger:     cfg.Logger,
		shutdownCh: make(chan struct{}),
		doneCh:     make(chan struct{}),
		pidFile:    cfg.PIDFile,

		reopenDaemonLog: cfg.ReopenDaemonLog,
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

	// A signal-driven shutdown arrives here without Shutdown(), which is
	// what sets the flag for the API path. Without it /healthz keeps
	// reporting ok and the API keeps accepting starts while everything stops.
	s.mu.Lock()
	s.shutting = true
	s.mu.Unlock()

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
	s.manager.CloseLogs()

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

// handleReload starts a SIGHUP reload in the background and returns a
// channel that is closed when it finishes (already closed when no reload
// ran). It must not block: the reload waits for changed programs to stop,
// and their exits are reaped by this same main-loop goroutine.
func (s *Supervisor) handleReload() <-chan struct{} {
	done := make(chan struct{})
	if !s.reloadMu.TryLock() {
		s.logger.Warn("reload already in progress; ignoring SIGHUP")
		close(done)
		return done
	}
	go func() {
		defer close(done)
		defer s.reloadMu.Unlock()
		if _, err := s.applyReload(); err != nil {
			s.logger.Error("reload failed", "error", err)
		}
	}()
	return done
}

// applyReload re-reads the config file and applies the difference through
// Manager.ApplyReload, which keeps running programs running and stopped ones
// stopped.
// The caller holds reloadMu.
func (s *Supervisor) applyReload() (config.ReloadResult, error) {
	s.mu.Lock()
	if s.shutting {
		s.mu.Unlock()
		s.logger.Warn("ignoring reload during shutdown")
		return config.ReloadResult{}, errors.New("daemon is shutting down")
	}
	oldCfg := s.config
	s.mu.Unlock()

	s.logger.Info("reloading config", "path", s.configPath)
	newCfg, warnings, err := config.LoadWithIncludes(s.configPath)
	if err != nil {
		return config.ReloadResult{}, err
	}
	for _, w := range warnings {
		s.logger.Warn("config warning", "warning", w)
	}

	added, changed, removed := process.ConfigDiff(oldCfg, newCfg)
	s.logger.Info("config diff", "added", added, "changed", changed, "removed", removed)
	s.warnWebChange(newCfg)

	// Programs that did not stop keep their old processes; record their old
	// config so the next reload sees them as changed or removed again.
	for _, prog := range s.manager.ApplyReload(oldCfg, newCfg, added, changed, removed) {
		if old, ok := oldCfg.Programs[prog]; ok {
			newCfg.Programs[prog] = old
		}
	}

	s.mu.Lock()
	s.config = newCfg
	s.mu.Unlock()
	return config.ReloadResult{Added: added, Changed: changed, Removed: removed, Warnings: warnings}, nil
}

// PreviewReload reports what a reload would change, and the warnings the
// new config produces, without applying it.
func (s *Supervisor) PreviewReload() (config.ReloadResult, error) {
	newCfg, warnings, err := config.LoadWithIncludes(s.configPath)
	if err != nil {
		return config.ReloadResult{}, err
	}
	s.mu.Lock()
	oldCfg := s.config
	s.mu.Unlock()
	added, changed, removed := process.ConfigDiff(oldCfg, newCfg)
	return config.ReloadResult{Added: added, Changed: changed, Removed: removed, Warnings: warnings}, nil
}

func (s *Supervisor) handleLogReopen() {
	s.logger.Info("reopening log files")
	if s.reopenDaemonLog != nil {
		if err := s.reopenDaemonLog(); err != nil {
			s.logger.Error("daemon log reopen failed", "error", err)
		}
	}
	s.manager.ReopenLogs()
	s.logger.Info("log files reopened")
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

	// The main loop no longer runs, so SIGCHLD must be handled here:
	// otherwise exited children stay zombies and never leave STOPPING.
	var sigC <-chan os.Signal
	if s.signals != nil {
		sigC = s.signals.C
	}

	for {
		select {
		case <-ctx.Done():
			s.logger.Warn("shutdown timeout exceeded, killing remaining processes")
			s.killRemaining()
			return
		case sig := <-sigC:
			switch sig {
			case syscall.SIGCHLD:
				s.handleSigchld()
				if s.allStopped() {
					return
				}
			case syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT:
				s.logger.Warn("signal received during shutdown, killing remaining processes", "signal", sig.String())
				s.killRemaining()
				return
			}
		case <-ticker.C:
			// Reap on every tick too: coalesced or dropped SIGCHLDs must
			// not stall shutdown.
			s.handleSigchld()
			if s.allStopped() {
				return
			}
		}
	}
}

// killReapGrace bounds how long killRemaining waits to reap SIGKILLed
// children. It is a var so tests can shorten it.
var killReapGrace = time.Second

// killRemaining sends SIGKILL to every process that has not stopped and
// reaps them, so no child outlives the daemon.
func (s *Supervisor) killRemaining() {
	for _, p := range s.manager.Processes() {
		if active(p.State()) {
			p.Kill()
		}
	}
	deadline := time.Now().Add(killReapGrace)
	for {
		s.handleSigchld()
		if s.allStopped() || time.Now().After(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *Supervisor) allStopped() bool {
	for _, p := range s.manager.Processes() {
		if active(p.State()) {
			return false
		}
	}
	return true
}

func active(state process.State) bool {
	return state == process.Running || state == process.Starting || state == process.Stopping
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
func (s *Supervisor) Reload() (config.ReloadResult, error) {
	// The API caller waits for a running reload rather than being refused.
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	return s.applyReload()
}

// warnWebChange logs when a reloaded [server.web] differs from the one the
// daemon started with: the UI is not rebuilt on reload.
func (s *Supervisor) warnWebChange(newCfg *config.Config) {
	if newCfg.Server.Web != s.startupWeb {
		s.logger.Warn("server.web changes take effect after restart")
	}
}
