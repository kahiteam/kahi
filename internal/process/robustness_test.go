package process

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
)

// A trampoline spawn (umask or rlimits) fails with the error a direct spawn
// gives, instead of starting and exiting 127 (FUNC-002).
func TestTrampolineSpawnErrorsMatchDirect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "worker"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The trampoline looks a bare name up in the program's environment
	// and directory, a direct spawn in the supervisor's (FUNC-087), so a
	// relative PATH hit is refused with exec.ErrDot's text there.
	for _, tc := range []struct {
		name, want string
		cfg        SpawnConfig
	}{
		{"missing path", "", SpawnConfig{Command: "/nonexistent/web"}},
		{"bare name not found", "", SpawnConfig{Command: "kahi-no-such-cmd", Env: []string{"PATH=/nonexistent"}}},
		{"relative PATH hit", `exec: "worker": cannot run executable found relative to current directory`, SpawnConfig{Command: "worker", Dir: dir, Env: []string{"PATH=/nonexistent:"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct := tc.cfg
			direct.Umask = -1
			_, derr := (&ExecSpawner{}).Spawn(direct)
			tramp := tc.cfg
			tramp.Umask = 0o22
			sp, terr := (&ExecSpawner{}).Spawn(tramp)
			if sp != nil {
				_ = sp.Signal(os.Kill)
			}
			want := tc.want
			if want == "" && derr != nil {
				want = derr.Error()
			}
			if derr == nil || terr == nil || terr.Error() != want {
				t.Fatalf("direct %v, trampoline %v; want both to fail, the trampoline with %q", derr, terr, want)
			}
		})
	}
	// A working trampoline spawn still runs the target with its pid.
	sp, err := (&ExecSpawner{}).Spawn(SpawnConfig{Command: "/bin/sh", Args: []string{"-c", "exit 0"}, Umask: 0o22})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := sp.Wait(); err != nil || st.ExitCode() != 0 {
		t.Errorf("trampoline target: %v %v", st, err)
	}
}

// A stdin write the child never reads fails after the stall instead of
// holding the process mutex (FUNC-077).
func TestWriteStdinFullDoesNotBlockProcess(t *testing.T) {
	old := sendStall
	sendStall = 300 * time.Millisecond
	t.Cleanup(func() { sendStall = old })
	cfg := defaultProgramConfig()
	cfg.Command = "/bin/sleep 30"
	cfg.Startsecs = 0
	p := NewProcess("t", "t", cfg, &ExecSpawner{}, testBus(), testLogger())
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Kill)
	errc := make(chan error, 1)
	go func() { errc <- p.WriteStdin(make([]byte, 1<<20)) }()
	pidc := make(chan int, 1)
	time.Sleep(100 * time.Millisecond)
	go func() { pidc <- p.Pid() }()
	select {
	case <-pidc:
	case <-time.After(time.Second):
		t.Fatal("Pid blocked behind a stdin write")
	}
	select {
	case err := <-errc:
		var full *StdinFullError
		if !errors.As(err, &full) || err.Error() != "process t: stdin is full (the process is not reading it)" {
			t.Errorf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stdin write did not give up")
	}
}

// An attach write blocked on a full pipe ends with the session.
func TestWriteAttachedCancelledByDetach(t *testing.T) {
	old := sendStall
	sendStall = 300 * time.Millisecond
	t.Cleanup(func() { sendStall = old })
	cfg := defaultProgramConfig()
	cfg.Command = "/bin/sleep 30"
	cfg.Startsecs = 0
	p := NewProcess("t", "t", cfg, &ExecSpawner{}, testBus(), testLogger())
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Kill)
	deadline := time.Now().Add(3 * time.Second)
	for p.State() != Running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := p.Attach(); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { errc <- p.WriteAttached(make([]byte, 1<<20)) }()
	time.Sleep(300 * time.Millisecond)
	p.Detach()
	select {
	case err := <-errc:
		if !errors.Is(err, errAttachEnded) {
			t.Errorf("err %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("attach write not cancelled by Detach")
	}
	if err := p.WriteStdin([]byte("x")); err != nil && !strings.Contains(err.Error(), "stdin is full") {
		t.Errorf("after detach: %v", err)
	}
}

// Raising startsecs by reload while STARTING extends the wait; the process
// still reaches RUNNING (it used to stay STARTING for good).
func TestStartsecsRaisedWhileStarting(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Startsecs = 1
	p := NewProcess("t", "t", cfg, &MockSpawner{}, testBus(), testLogger())
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	cfg2 := cfg
	cfg2.Startsecs = 2
	p.UpdateConfig(cfg2)
	time.Sleep(1200 * time.Millisecond)
	if s := p.State(); s != Starting {
		t.Fatalf("after 1.4s with startsecs 2: %s, want STARTING", s)
	}
	deadline := time.Now().Add(3 * time.Second)
	for p.State() != Running && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if s := p.State(); s != Running {
		t.Errorf("state %s, want RUNNING", s)
	}
}

// A manual start after FATAL gets the full startretries window again.
func TestManualStartAfterFatalResetsRetries(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Startretries = 1
	sp := &MockSpawner{SpawnFn: func(c SpawnConfig) (SpawnedProcess, error) { return nil, errors.New("boom") }}
	p := NewProcess("t", "t", cfg, sp, testBus(), testLogger())
	waitFatal := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for p.State() != Fatal && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if p.State() != Fatal {
			t.Fatalf("state %s, want FATAL", p.State())
		}
	}
	_ = p.Start()
	waitFatal()
	first := len(sp.SpawnCalls)
	_ = p.Start()
	waitFatal()
	if again := len(sp.SpawnCalls) - first; again != first {
		t.Errorf("first run %d attempts, manual start %d; want the same", first, again)
	}
}

// The backoff delay stays at the 60 s cap for any retry count.
func TestBackoffDelayNoOverflow(t *testing.T) {
	sm := NewStateMachine(StateMachineConfig{Startretries: 1000})
	for _, r := range []int{1, 6, 7, 35, 40, 64, 1000} {
		sm.retries = r
		d := sm.BackoffDelay()
		if d <= 0 || d > 60*time.Second || (r >= 7 && d != 60*time.Second) {
			t.Errorf("retries %d: delay %v", r, d)
		}
	}
}

// Restart waits stopwaitsecs plus the margin and reports a process that
// is still stopping, instead of a fixed 30 s and a failed start.
func TestRestartWaitsStopwaitsecs(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Startsecs = 0
	cfg.Stopwaitsecs = 1
	m := NewManager(&MockSpawner{}, testBus(), testLogger()) // mock children never exit
	m.LoadConfig(reloadCfg(map[string]config.ProgramConfig{"web": cfg}))
	if err := m.Start("web"); err != nil {
		t.Fatal(err)
	}
	p, _ := m.GetProcess("web")
	deadline := time.Now().Add(3 * time.Second)
	for p.State() != Running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	err := m.Restart("web")
	if err == nil || err.Error() != "process web did not stop within 6s" {
		t.Errorf("restart: %v", err)
	}
	if d := time.Since(start); d < 5*time.Second || d > 10*time.Second {
		t.Errorf("restart waited %v, want about 6s", d)
	}
}
