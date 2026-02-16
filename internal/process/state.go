package process

import (
	"fmt"
	"sync"
	"time"
)

// State represents a process lifecycle state.
type State int

const (
	Stopped  State = iota // STOPPED: not running
	Starting              // STARTING: process launched, waiting for startsecs
	Running               // RUNNING: successfully started
	Backoff               // BACKOFF: failed to start, waiting to retry
	Stopping              // STOPPING: stop signal sent, waiting for exit
	Exited                // EXITED: process exited on its own from RUNNING
	Fatal                 // FATAL: could not be started successfully
)

var stateNames = [...]string{
	"STOPPED", "STARTING", "RUNNING", "BACKOFF", "STOPPING", "EXITED", "FATAL",
}

func (s State) String() string {
	if int(s) < len(stateNames) {
		return stateNames[s]
	}
	return fmt.Sprintf("UNKNOWN(%d)", s)
}

// validTransitions defines allowed state transitions.
var validTransitions = map[State][]State{
	Stopped:  {Starting},
	Starting: {Running, Backoff, Stopping},
	Running:  {Stopping, Exited},
	Backoff:  {Starting, Fatal},
	Stopping: {Stopped, Exited},
	Exited:   {Starting},
	Fatal:    {Starting},
}

// Clock abstracts time for testability.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// realClock uses the system clock.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// RealClock returns a Clock backed by the system clock.
func RealClock() Clock { return realClock{} }

// StateMachine manages process state transitions.
type StateMachine struct {
	mu         sync.Mutex
	state      State
	retries    int
	maxRetries int
	startsecs  time.Duration
	startedAt  time.Time
	clock      Clock
	manualStop bool // true if current stop was user-initiated
}

// StateMachineConfig configures a state machine.
type StateMachineConfig struct {
	Startsecs    int // seconds before STARTING->RUNNING
	Startretries int // max retries before FATAL
	Clock        Clock
}

// NewStateMachine creates a state machine in STOPPED state.
func NewStateMachine(cfg StateMachineConfig) *StateMachine {
	clk := cfg.Clock
	if clk == nil {
		clk = RealClock()
	}
	return &StateMachine{
		state:      Stopped,
		maxRetries: cfg.Startretries,
		startsecs:  time.Duration(cfg.Startsecs) * time.Second,
		clock:      clk,
	}
}

// State returns the current state.
func (sm *StateMachine) State() State {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.state
}

// Retries returns the current backoff retry count.
func (sm *StateMachine) Retries() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.retries
}

// ManualStop returns whether the last stop was user-initiated.
func (sm *StateMachine) ManualStop() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.manualStop
}

// Transition attempts a state transition. Returns an error if the
// transition is invalid.
func (sm *StateMachine) Transition(target State) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.transitionLocked(target)
}

func (sm *StateMachine) transitionLocked(target State) error {
	allowed := validTransitions[sm.state]
	for _, a := range allowed {
		if a == target {
			sm.applyTransition(target)
			return nil
		}
	}
	return fmt.Errorf("cannot transition from %s to %s", sm.state, target)
}

func (sm *StateMachine) applyTransition(target State) {
	sm.state = target

	switch target {
	case Starting:
		sm.startedAt = sm.clock.Now()
		// Retries reset on reaching Running, not on fresh start.
	case Running:
		sm.retries = 0 // reset on successful start
	case Backoff:
		sm.retries++
	case Stopping:
		// manualStop is set by RequestStop, not here
	case Stopped:
		// nothing extra
	case Exited:
		// nothing extra
	case Fatal:
		// nothing extra
	}
}

// RequestStart transitions from STOPPED/EXITED/FATAL to STARTING.
func (sm *StateMachine) RequestStart() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.manualStop = false
	return sm.transitionLocked(Starting)
}

// RequestStop transitions from RUNNING to STOPPING (user-initiated).
func (sm *StateMachine) RequestStop() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.manualStop = true
	return sm.transitionLocked(Stopping)
}

// ProcessStarted checks whether startsecs has elapsed since STARTING.
// If so, transitions to RUNNING. Returns the new state.
func (sm *StateMachine) ProcessStarted() (State, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.state != Starting {
		return sm.state, nil
	}

	elapsed := sm.clock.Now().Sub(sm.startedAt)
	if elapsed >= sm.startsecs {
		if err := sm.transitionLocked(Running); err != nil {
			return sm.state, err
		}
	}
	return sm.state, nil
}

// ProcessExitedEarly transitions STARTING->BACKOFF or BACKOFF->FATAL
// when a process exits before startsecs.
func (sm *StateMachine) ProcessExitedEarly() (State, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.state != Starting {
		return sm.state, fmt.Errorf("ProcessExitedEarly called in %s state", sm.state)
	}

	if err := sm.transitionLocked(Backoff); err != nil {
		return sm.state, err
	}

	if sm.retries > sm.maxRetries {
		if err := sm.transitionLocked(Fatal); err != nil {
			return sm.state, err
		}
	}
	return sm.state, nil
}

// ProcessExited transitions RUNNING->EXITED or STOPPING->STOPPED/EXITED.
func (sm *StateMachine) ProcessExited() (State, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	switch sm.state {
	case Running:
		return sm.state, sm.transitionLocked(Exited)
	case Stopping:
		// Whether the process exited from stop signal or on its own,
		// transition to STOPPED.
		return sm.state, sm.transitionLocked(Stopped)
	default:
		return sm.state, fmt.Errorf("ProcessExited called in %s state", sm.state)
	}
}

// BackoffDelay returns the backoff delay for the current retry count.
// Uses exponential backoff: 2^(retries-1) seconds, capped at 60s.
func (sm *StateMachine) BackoffDelay() time.Duration {
	sm.mu.Lock()
	r := sm.retries
	sm.mu.Unlock()

	if r <= 0 {
		return time.Second
	}
	d := time.Second << uint(r-1) // 2^(r-1) seconds
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// RetryFromBackoff transitions BACKOFF->STARTING for a retry attempt.
func (sm *StateMachine) RetryFromBackoff() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.state != Backoff {
		return fmt.Errorf("RetryFromBackoff called in %s state", sm.state)
	}
	return sm.transitionLocked(Starting)
}
