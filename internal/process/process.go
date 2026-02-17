// Package process manages individual OS process lifecycle.
package process

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
)

// Process represents a managed child process.
type Process struct {
	mu         sync.Mutex
	name       string
	group      string
	config     config.ProgramConfig
	sm         *StateMachine
	spawner    ProcessSpawner
	spawned    SpawnedProcess
	exitCode   int
	startedAt  time.Time
	bus        *events.Bus
	logger     *slog.Logger
	stopCh     chan struct{} // signals the stop-wait goroutine
	killDoneCh chan struct{} // closed when process exits, cancels SIGKILL timer
	shutdownCh chan struct{} // closed on daemon shutdown
	onStdout   func(name string, data []byte)
	onStderr   func(name string, data []byte)
}

// ProcessOption configures a Process.
type ProcessOption func(*Process)

// WithStdoutHandler sets a callback for stdout data.
func WithStdoutHandler(fn func(name string, data []byte)) ProcessOption {
	return func(p *Process) { p.onStdout = fn }
}

// WithStderrHandler sets a callback for stderr data.
func WithStderrHandler(fn func(name string, data []byte)) ProcessOption {
	return func(p *Process) { p.onStderr = fn }
}

// WithShutdownCh sets the daemon shutdown channel.
func WithShutdownCh(ch chan struct{}) ProcessOption {
	return func(p *Process) { p.shutdownCh = ch }
}

// NewProcess creates a managed process from config.
func NewProcess(name, group string, cfg config.ProgramConfig, spawner ProcessSpawner, bus *events.Bus, logger *slog.Logger, opts ...ProcessOption) *Process {
	sm := NewStateMachine(StateMachineConfig{
		Startsecs:    cfg.Startsecs,
		Startretries: cfg.Startretries,
	})

	p := &Process{
		name:       name,
		group:      group,
		config:     cfg,
		sm:         sm,
		spawner:    spawner,
		bus:        bus,
		logger:     logger.With("process", name),
		stopCh:     make(chan struct{}),
		shutdownCh: make(chan struct{}),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// Name returns the process name.
func (p *Process) Name() string { return p.name }

// Group returns the process group name.
func (p *Process) Group() string { return p.group }

// Config returns the process configuration.
func (p *Process) Config() config.ProgramConfig { return p.config }

// State returns the current state.
func (p *Process) State() State { return p.sm.State() }

// Pid returns the process ID, or 0 if not running.
func (p *Process) Pid() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pidLocked()
}

func (p *Process) pidLocked() int {
	if p.spawned != nil {
		return p.spawned.Pid()
	}
	return 0
}

// ExitCode returns the last exit code.
func (p *Process) ExitCode() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitCode
}

// StartedAt returns when the process was last started.
func (p *Process) StartedAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startedAt
}

// Uptime returns seconds since last start, or 0 if not running.
func (p *Process) Uptime() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.startedAt.IsZero() {
		return 0
	}
	if p.sm.State() == Running || p.sm.State() == Starting {
		return int64(time.Since(p.startedAt).Seconds())
	}
	return 0
}

// Start spawns the child process.
func (p *Process) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startLocked()
}

func (p *Process) startLocked() error {
	if err := p.sm.RequestStart(); err != nil {
		return fmt.Errorf("process %s: %w", p.name, err)
	}

	p.publishStateLocked(Starting)
	return p.spawnLocked()
}

// spawnLocked does the actual spawn without state transitions.
// Called by startLocked (after RequestStart) and retryAfterBackoff (after RetryFromBackoff).
func (p *Process) spawnLocked() error {
	env := p.buildEnv()
	cmd, args := p.parseCommand()

	spawnCfg := SpawnConfig{
		Command: cmd,
		Args:    args,
		Dir:     p.config.Directory,
		Env:     env,
	}

	// Apply resource limits.
	rlimits := ParseRLimits(p.config)
	if len(rlimits) > 0 {
		spawnCfg.RLimits = rlimits
	}

	spawned, err := p.spawner.Spawn(spawnCfg)
	if err != nil {
		p.logger.Error("spawn failed", "error", err)
		// Transition to backoff or fatal.
		newState, fErr := p.sm.ProcessExitedEarly()
		if fErr != nil {
			p.logger.Error("state transition failed", "error", fErr)
		}
		p.publishStateLocked(p.sm.State())

		// If in backoff, schedule a retry (HandleExit won't fire since no child was created).
		if newState == Backoff {
			go p.retryAfterBackoff(p.stopCh)
		}

		return fmt.Errorf("process %s: spawn failed: %w", p.name, err)
	}

	p.spawned = spawned
	p.startedAt = time.Now()
	p.logger.Info("started", "pid", spawned.Pid())

	// Start pipe readers (check pipe non-nil for mock spawners).
	if p.onStdout != nil {
		if pipe := spawned.StdoutPipe(); pipe != nil {
			go p.readPipe(pipe, "stdout", p.onStdout)
		}
		// When redirect_stderr=true, route stderr through the stdout handler.
		if p.config.RedirectStderr {
			if pipe := spawned.StderrPipe(); pipe != nil {
				go p.readPipe(pipe, "stderr", p.onStdout)
			}
		}
	}
	if p.onStderr != nil && !p.config.RedirectStderr {
		if pipe := spawned.StderrPipe(); pipe != nil {
			go p.readPipe(pipe, "stderr", p.onStderr)
		}
	}

	// For startsecs=0, transition to RUNNING synchronously to avoid a race
	// where SIGCHLD arrives before the watchStart goroutine runs.
	if p.config.Startsecs == 0 {
		if _, err := p.sm.ProcessStarted(); err != nil {
			p.logger.Error("state transition failed", "error", err)
		}
		p.publishStateLocked(p.sm.State())
	} else {
		go p.watchStart(p.stopCh)
	}

	return nil
}

func (p *Process) watchStart(stopCh <-chan struct{}) {
	startsecs := time.Duration(p.config.Startsecs) * time.Second
	if startsecs == 0 {
		// Immediate transition to RUNNING.
		if _, err := p.sm.ProcessStarted(); err != nil {
			p.logger.Error("state transition failed", "error", err)
		}
		p.publishStateUnlocked(p.sm.State())
		return
	}

	timer := time.NewTimer(startsecs)
	defer timer.Stop()

	select {
	case <-timer.C:
		if _, err := p.sm.ProcessStarted(); err != nil {
			p.logger.Error("state transition failed", "error", err)
		}
		p.publishStateUnlocked(p.sm.State())
	case <-stopCh:
		return
	}
}

// Stop sends the configured stop signal and waits for exit.
func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopLocked()
}

func (p *Process) stopLocked() error {
	state := p.sm.State()

	// Allow stopping from Backoff: cancel pending retry and go to STOPPED.
	if state == Backoff {
		if err := p.sm.Transition(Stopped); err != nil {
			return fmt.Errorf("process %s: %w", p.name, err)
		}
		p.publishStateLocked(Stopped)
		select {
		case <-p.stopCh:
		default:
			close(p.stopCh)
		}
		p.stopCh = make(chan struct{})
		return nil
	}

	if state != Running && state != Starting {
		return fmt.Errorf("process %s: not running", p.name)
	}

	if err := p.sm.RequestStop(); err != nil {
		return fmt.Errorf("process %s: %w", p.name, err)
	}

	p.publishStateLocked(Stopping)

	// Close stop channel to signal watchers (watchStart, retryAfterBackoff).
	select {
	case <-p.stopCh:
	default:
		close(p.stopCh)
	}

	sig := ParseSignal(p.config.Stopsignal)
	if p.spawned != nil {
		if p.config.Stopasgroup {
			// Send to process group.
			_ = syscall.Kill(-p.spawned.Pid(), sig.(syscall.Signal))
		} else {
			_ = p.spawned.Signal(sig)
		}
	}

	// Start the kill escalation timer with a dedicated channel
	// (stopCh is already closed and can't be reused).
	killDone := make(chan struct{})
	p.killDoneCh = killDone
	go p.watchStop(killDone)

	return nil
}

func (p *Process) watchStop(stopCh <-chan struct{}) {
	timeout := time.Duration(p.config.Stopwaitsecs) * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		p.mu.Lock()
		if p.sm.State() == Stopping && p.spawned != nil {
			p.logger.Warn("escalating to SIGKILL", "pid", p.spawned.Pid())
			if p.config.Killasgroup {
				_ = syscall.Kill(-p.spawned.Pid(), syscall.SIGKILL)
			} else {
				_ = p.spawned.Signal(syscall.SIGKILL)
			}
		}
		p.mu.Unlock()
	case <-stopCh:
		// Process exited before timeout.
	}
}

// Signal sends an arbitrary signal to the process.
func (p *Process) Signal(sig string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.spawned == nil {
		return fmt.Errorf("process %s: not running", p.name)
	}

	s := ParseSignal(sig)
	if s == nil {
		return fmt.Errorf("process %s: invalid signal %q", p.name, sig)
	}

	return p.spawned.Signal(s)
}

// WriteStdin writes data to the process stdin pipe.
func (p *Process) WriteStdin(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.spawned == nil {
		return fmt.Errorf("process %s: not running", p.name)
	}

	pipe := p.spawned.StdinPipe()
	if pipe == nil {
		return fmt.Errorf("process %s: does not accept stdin", p.name)
	}

	_, err := pipe.Write(data)
	return err
}

// HandleExit processes the exit of a child process with the given exit code.
func (p *Process) HandleExit(exitCode int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.exitCode = exitCode
	p.logger.Info("exited", "exit_code", exitCode)

	state := p.sm.State()
	switch state {
	case Starting:
		// Exited before startsecs -- backoff or fatal.
		newState, err := p.sm.ProcessExitedEarly()
		if err != nil {
			p.logger.Error("state transition failed", "error", err)
		}
		p.publishStateLocked(newState)

		if newState == Backoff {
			go p.retryAfterBackoff(p.stopCh)
		}
	case Running:
		// Exited from running state.
		if _, err := p.sm.ProcessExited(); err != nil {
			p.logger.Error("state transition failed", "error", err)
		}
		p.publishStateLocked(Exited)

		// Check autorestart.
		if p.shouldRestart(exitCode) {
			go p.restartAfterExit()
		}
	case Stopping:
		// Expected exit from stop.
		if _, err := p.sm.ProcessExited(); err != nil {
			p.logger.Error("state transition failed", "error", err)
		}
		p.publishStateLocked(Stopped)
	default:
		p.logger.Warn("unexpected exit in state", "state", state.String())
	}

	// Cancel the SIGKILL escalation timer if running.
	if p.killDoneCh != nil {
		close(p.killDoneCh)
		p.killDoneCh = nil
	}

	// Reset spawned reference.
	p.spawned = nil
	// Reset stop channel for next start.
	p.stopCh = make(chan struct{})
}

func (p *Process) shouldRestart(exitCode int) bool {
	// Never restart during shutdown.
	select {
	case <-p.shutdownCh:
		return false
	default:
	}

	// Never restart manually stopped processes.
	if p.sm.ManualStop() {
		return false
	}

	switch p.config.Autorestart {
	case "true":
		return true
	case "false":
		return false
	case "unexpected":
		for _, code := range p.config.Exitcodes {
			if exitCode == code {
				return false // Expected exit code, don't restart.
			}
		}
		return true
	default:
		return false
	}
}

func (p *Process) retryAfterBackoff(stopCh <-chan struct{}) {
	delay := p.sm.BackoffDelay()
	p.logger.Info("backing off", "delay", delay, "retries", p.sm.Retries())

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		p.mu.Lock()
		if p.sm.State() != Backoff {
			p.mu.Unlock()
			return
		}
		if err := p.sm.RetryFromBackoff(); err != nil {
			p.logger.Error("retry transition failed", "error", err)
			p.mu.Unlock()
			return
		}
		p.publishStateLocked(Starting)
		if err := p.spawnLocked(); err != nil {
			p.logger.Error("retry start failed", "error", err)
		}
		p.mu.Unlock()
	case <-stopCh:
		return
	}
}

func (p *Process) restartAfterExit() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.sm.State() != Exited {
		return
	}

	p.logger.Info("autorestarting")
	if err := p.startLocked(); err != nil {
		p.logger.Error("restart failed", "error", err)
	}
}

func (p *Process) buildEnv() []string {
	var env []string

	if p.config.CleanEnvironment {
		// Only supervisor vars + explicit environment.
		env = []string{}
	} else {
		// Inherit parent environment.
		env = os.Environ()
	}

	// Add supervisor identification vars.
	env = append(env,
		"SUPERVISOR_ENABLED=1",
		fmt.Sprintf("SUPERVISOR_PROCESS_NAME=%s", p.name),
		fmt.Sprintf("SUPERVISOR_GROUP_NAME=%s", p.group),
	)

	// Add per-process configured vars.
	for k, v := range p.config.Environment {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	return env
}

func (p *Process) parseCommand() (string, []string) {
	parts := strings.Fields(p.config.Command)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

func (p *Process) readPipe(r io.ReadCloser, stream string, handler func(string, []byte)) {
	buf := make([]byte, 8192)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			handler(p.name, data)
		}
		if err != nil {
			return
		}
	}
}

// publishStateLocked publishes a state event while the mutex is held.
func (p *Process) publishStateLocked(state State) {
	p.doPublishState(state, p.pidLocked())
}

// publishStateUnlocked publishes a state event, acquiring the mutex for PID.
func (p *Process) publishStateUnlocked(state State) {
	p.mu.Lock()
	pid := p.pidLocked()
	p.mu.Unlock()
	p.doPublishState(state, pid)
}

func (p *Process) doPublishState(state State, pid int) {
	if p.bus == nil {
		return
	}

	var eventType events.EventType
	switch state {
	case Stopped:
		eventType = events.ProcessStateStopped
	case Starting:
		eventType = events.ProcessStateStarting
	case Running:
		eventType = events.ProcessStateRunning
	case Backoff:
		eventType = events.ProcessStateBackoff
	case Stopping:
		eventType = events.ProcessStateStopping
	case Exited:
		eventType = events.ProcessStateExited
	case Fatal:
		eventType = events.ProcessStateFatal
	default:
		return
	}

	p.bus.Publish(events.Event{
		Type: eventType,
		Data: map[string]string{
			"name":  p.name,
			"group": p.group,
			"state": state.String(),
			"pid":   fmt.Sprintf("%d", pid),
		},
	})
}

// ParseSignal converts a signal name to os.Signal.
func ParseSignal(name string) os.Signal {
	name = strings.TrimPrefix(strings.ToUpper(name), "SIG")
	switch name {
	case "TERM":
		return syscall.SIGTERM
	case "HUP":
		return syscall.SIGHUP
	case "INT":
		return syscall.SIGINT
	case "QUIT":
		return syscall.SIGQUIT
	case "KILL":
		return syscall.SIGKILL
	case "USR1":
		return syscall.SIGUSR1
	case "USR2":
		return syscall.SIGUSR2
	case "STOP":
		return syscall.SIGSTOP
	case "CONT":
		return syscall.SIGCONT
	default:
		return nil
	}
}
