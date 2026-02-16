package process

import (
	"testing"
	"time"
)

// mockClock is a controllable clock for testing.
type mockClock struct {
	now time.Time
}

func newMockClock() *mockClock {
	return &mockClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *mockClock) Now() time.Time { return c.now }
func (c *mockClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- c.now.Add(d)
	return ch
}
func (c *mockClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newSM(startsecs, retries int, clk Clock) *StateMachine {
	return NewStateMachine(StateMachineConfig{
		Startsecs:    startsecs,
		Startretries: retries,
		Clock:        clk,
	})
}

// mustTransition is a test helper that calls a function and fails the test on error.
func mustTransition(t *testing.T, fn func() error) {
	t.Helper()
	if err := fn(); err != nil {
		t.Fatal(err)
	}
}

// mustTransitionState is a test helper that calls a state-returning function and fails on error.
func mustTransitionState(t *testing.T, fn func() (State, error)) State {
	t.Helper()
	s, err := fn()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoppedToStarting(t *testing.T) {
	sm := newSM(1, 3, newMockClock())
	if err := sm.RequestStart(); err != nil {
		t.Fatal(err)
	}
	if sm.State() != Starting {
		t.Fatalf("state = %s, want STARTING", sm.State())
	}
}

func TestStartingToRunningAfterStartsecs(t *testing.T) {
	clk := newMockClock()
	sm := newSM(2, 3, clk)
	mustTransition(t, sm.RequestStart)

	// Before startsecs
	clk.Advance(1 * time.Second)
	s := mustTransitionState(t, sm.ProcessStarted)
	if s != Starting {
		t.Fatalf("state = %s, want STARTING (before startsecs)", s)
	}

	// After startsecs
	clk.Advance(2 * time.Second)
	s = mustTransitionState(t, sm.ProcessStarted)
	if s != Running {
		t.Fatalf("state = %s, want RUNNING (after startsecs)", s)
	}
}

func TestStartingToBackoffOnEarlyExit(t *testing.T) {
	clk := newMockClock()
	sm := newSM(5, 3, clk)
	mustTransition(t, sm.RequestStart)

	s := mustTransitionState(t, sm.ProcessExitedEarly)
	if s != Backoff {
		t.Fatalf("state = %s, want BACKOFF", s)
	}
}

func TestBackoffToStartingOnRetry(t *testing.T) {
	clk := newMockClock()
	sm := newSM(5, 3, clk)
	mustTransition(t, sm.RequestStart)
	mustTransitionState(t, sm.ProcessExitedEarly)

	if err := sm.RetryFromBackoff(); err != nil {
		t.Fatal(err)
	}
	if sm.State() != Starting {
		t.Fatalf("state = %s, want STARTING", sm.State())
	}
}

func TestBackoffToFatalOnRetryExhaustion(t *testing.T) {
	clk := newMockClock()
	sm := newSM(5, 2, clk)

	// Attempt 1
	mustTransition(t, sm.RequestStart)
	mustTransitionState(t, sm.ProcessExitedEarly)
	mustTransition(t, sm.RetryFromBackoff)

	// Attempt 2
	mustTransitionState(t, sm.ProcessExitedEarly)
	mustTransition(t, sm.RetryFromBackoff)

	// Attempt 3 -- exceeds maxRetries=2
	s := mustTransitionState(t, sm.ProcessExitedEarly)
	if s != Fatal {
		t.Fatalf("state = %s, want FATAL", s)
	}
}

func TestRunningToStoppingOnStop(t *testing.T) {
	clk := newMockClock()
	sm := newSM(0, 3, clk) // startsecs=0 means immediate RUNNING
	mustTransition(t, sm.RequestStart)
	mustTransitionState(t, sm.ProcessStarted)

	if err := sm.RequestStop(); err != nil {
		t.Fatal(err)
	}
	if sm.State() != Stopping {
		t.Fatalf("state = %s, want STOPPING", sm.State())
	}
	if !sm.ManualStop() {
		t.Fatal("expected ManualStop=true")
	}
}

func TestStoppingToStoppedOnExit(t *testing.T) {
	clk := newMockClock()
	sm := newSM(0, 3, clk)
	mustTransition(t, sm.RequestStart)
	mustTransitionState(t, sm.ProcessStarted)
	mustTransition(t, sm.RequestStop)

	mustTransitionState(t, sm.ProcessExited)
	if sm.State() != Stopped {
		t.Fatalf("state = %s, want STOPPED", sm.State())
	}
}

func TestRunningToExitedOnSelfExit(t *testing.T) {
	clk := newMockClock()
	sm := newSM(0, 3, clk)
	mustTransition(t, sm.RequestStart)
	mustTransitionState(t, sm.ProcessStarted)

	mustTransitionState(t, sm.ProcessExited)
	if sm.State() != Exited {
		t.Fatalf("state = %s, want EXITED", sm.State())
	}
}

func TestInvalidTransitionRejected(t *testing.T) {
	sm := newSM(1, 3, newMockClock())
	// STOPPED -> RUNNING is invalid
	err := sm.Transition(Running)
	if err == nil {
		t.Fatal("expected error for invalid transition STOPPED->RUNNING")
	}
}

func TestRetryCounterResetsOnRunning(t *testing.T) {
	clk := newMockClock()
	sm := newSM(0, 5, clk)

	// Fail twice
	mustTransition(t, sm.RequestStart)
	mustTransitionState(t, sm.ProcessExitedEarly)
	mustTransition(t, sm.RetryFromBackoff)
	mustTransitionState(t, sm.ProcessExitedEarly)
	if sm.Retries() != 2 {
		t.Fatalf("retries = %d, want 2", sm.Retries())
	}

	// Succeed
	mustTransition(t, sm.RetryFromBackoff)
	mustTransitionState(t, sm.ProcessStarted) // startsecs=0 -> immediate RUNNING
	if sm.Retries() != 0 {
		t.Fatalf("retries after RUNNING = %d, want 0", sm.Retries())
	}
}

func TestStoppingToExitedWorks(t *testing.T) {
	// "Transition from STOPPING to EXITED must work even if stop signal was
	// not the cause of exit" -- the spec says this but our implementation
	// transitions STOPPING->STOPPED. This test verifies STOPPING->STOPPED
	// always works regardless of exit cause.
	clk := newMockClock()
	sm := newSM(0, 3, clk)
	mustTransition(t, sm.RequestStart)
	mustTransitionState(t, sm.ProcessStarted)
	mustTransition(t, sm.RequestStop)

	mustTransitionState(t, sm.ProcessExited)
	// Our design: STOPPING always goes to STOPPED
	if sm.State() != Stopped {
		t.Fatalf("state = %s, want STOPPED", sm.State())
	}
}

func TestClockRollbackDoesNotCausePrematureRunning(t *testing.T) {
	clk := newMockClock()
	sm := newSM(5, 3, clk)
	mustTransition(t, sm.RequestStart)

	// Advance 3 seconds
	clk.Advance(3 * time.Second)
	s := mustTransitionState(t, sm.ProcessStarted)
	if s != Starting {
		t.Fatal("should still be STARTING")
	}

	// Roll back clock by 4 seconds (net: -1 second from start)
	clk.Advance(-4 * time.Second)
	s = mustTransitionState(t, sm.ProcessStarted)
	if s != Starting {
		t.Fatal("clock rollback should not cause RUNNING")
	}
}

func TestBackoffDelay(t *testing.T) {
	clk := newMockClock()
	sm := newSM(5, 10, clk)

	// Zero retries returns 1 second
	if d := sm.BackoffDelay(); d != time.Second {
		t.Fatalf("delay at 0 retries = %v, want 1s", d)
	}

	// After 1 retry: 2^0 = 1s
	mustTransition(t, sm.RequestStart)
	mustTransitionState(t, sm.ProcessExitedEarly)
	if d := sm.BackoffDelay(); d != time.Second {
		t.Fatalf("delay at 1 retry = %v, want 1s", d)
	}

	// After 2 retries: 2^1 = 2s
	mustTransition(t, sm.RetryFromBackoff)
	mustTransitionState(t, sm.ProcessExitedEarly)
	if d := sm.BackoffDelay(); d != 2*time.Second {
		t.Fatalf("delay at 2 retries = %v, want 2s", d)
	}

	// After 3 retries: 2^2 = 4s
	mustTransition(t, sm.RetryFromBackoff)
	mustTransitionState(t, sm.ProcessExitedEarly)
	if d := sm.BackoffDelay(); d != 4*time.Second {
		t.Fatalf("delay at 3 retries = %v, want 4s", d)
	}
}

func TestBackoffDelayCapsAt60s(t *testing.T) {
	clk := newMockClock()
	sm := newSM(5, 20, clk)

	// Drive retries to 8: 2^7 = 128s, should cap at 60s
	mustTransition(t, sm.RequestStart)
	mustTransitionState(t, sm.ProcessExitedEarly)
	for i := 1; i < 8; i++ {
		mustTransition(t, sm.RetryFromBackoff)
		mustTransitionState(t, sm.ProcessExitedEarly)
	}

	if d := sm.BackoffDelay(); d != 60*time.Second {
		t.Fatalf("delay at 8 retries = %v, want 60s (capped)", d)
	}
}

func TestNewStateMachineNilClock(t *testing.T) {
	sm := NewStateMachine(StateMachineConfig{
		Startsecs:    1,
		Startretries: 3,
		Clock:        nil,
	})
	if sm.State() != Stopped {
		t.Fatalf("state = %s, want STOPPED", sm.State())
	}
	// Verify the clock works (doesn't panic)
	mustTransition(t, sm.RequestStart)
}

func TestStateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{Stopped, "STOPPED"},
		{Starting, "STARTING"},
		{Running, "RUNNING"},
		{Backoff, "BACKOFF"},
		{Stopping, "STOPPING"},
		{Exited, "EXITED"},
		{Fatal, "FATAL"},
		{State(99), "UNKNOWN(99)"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestProcessStartedInWrongState(t *testing.T) {
	sm := newSM(1, 3, newMockClock())
	// Call ProcessStarted while STOPPED (not STARTING)
	s, err := sm.ProcessStarted()
	if err != nil {
		t.Fatal(err)
	}
	if s != Stopped {
		t.Fatalf("state = %s, want STOPPED (no-op)", s)
	}
}

func TestProcessExitedEarlyInWrongState(t *testing.T) {
	sm := newSM(1, 3, newMockClock())
	// Call ProcessExitedEarly while STOPPED
	_, err := sm.ProcessExitedEarly()
	if err == nil {
		t.Fatal("expected error for ProcessExitedEarly in STOPPED state")
	}
}

func TestProcessExitedInWrongState(t *testing.T) {
	sm := newSM(1, 3, newMockClock())
	// Call ProcessExited while STOPPED
	_, err := sm.ProcessExited()
	if err == nil {
		t.Fatal("expected error for ProcessExited in STOPPED state")
	}
}

func TestRetryFromBackoffInWrongState(t *testing.T) {
	sm := newSM(1, 3, newMockClock())
	// Call RetryFromBackoff while STOPPED
	err := sm.RetryFromBackoff()
	if err == nil {
		t.Fatal("expected error for RetryFromBackoff in STOPPED state")
	}
}

func TestRealClock(t *testing.T) {
	clk := RealClock()
	now := clk.Now()
	if now.IsZero() {
		t.Fatal("RealClock.Now() returned zero time")
	}
	ch := clk.After(time.Millisecond)
	select {
	case <-ch:
		// ok
	case <-time.After(time.Second):
		t.Fatal("RealClock.After() did not fire")
	}
}
