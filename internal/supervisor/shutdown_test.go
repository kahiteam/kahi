package supervisor

import (
	"io"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/process"
)

// fakeChild is a SpawnedProcess that "exits" when it receives a signal it
// does not ignore. Exited children are handed out once by reap.
type fakeChild struct {
	pid      int
	ignoreTE bool

	mu      sync.Mutex
	signals []os.Signal
	exited  bool
	reaped  bool
}

func (c *fakeChild) Pid() int                        { return c.pid }
func (c *fakeChild) Wait() (*os.ProcessState, error) { select {} }
func (c *fakeChild) StdinPipe() io.WriteCloser       { return nil }
func (c *fakeChild) StdoutPipe() io.ReadCloser       { return nil }
func (c *fakeChild) StderrPipe() io.ReadCloser       { return nil }

func (c *fakeChild) Signal(sig os.Signal) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.signals = append(c.signals, sig)
	if sig == syscall.SIGKILL || !c.ignoreTE {
		c.exited = true
	}
	return nil
}

func (c *fakeChild) gotKill() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.signals {
		if s == syscall.SIGKILL {
			return true
		}
	}
	return false
}

// reap mimics a WNOHANG waitpid for this one child.
func (c *fakeChild) reap() (int, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exited && !c.reaped {
		c.reaped = true
		return c.pid, 0, nil
	}
	return 0, 0, nil
}

// shutdownFixture starts one program backed by child and begins shutdown
// the way Run does, by calling StopAll.
func shutdownFixture(t *testing.T, child *fakeChild, shutdownTimeout int) (*Supervisor, *process.Process) {
	t.Helper()
	autostart := true
	cfg := &config.Config{
		Supervisor: config.SupervisorConfig{ShutdownTimeout: shutdownTimeout},
		Programs: map[string]config.ProgramConfig{
			"worker": {
				Command:      "/bin/sleep 300",
				Numprocs:     1,
				Priority:     999,
				Autostart:    &autostart,
				Autorestart:  "false",
				Exitcodes:    []int{0},
				Stopsignal:   "TERM",
				Stopwaitsecs: 60,
			},
		},
	}
	spawner := &process.MockSpawner{
		SpawnFn: func(process.SpawnConfig) (process.SpawnedProcess, error) { return child, nil },
	}
	s := testSupervisorWithMock(cfg, spawner)
	s.manager.LoadConfig(cfg)
	p := s.manager.Processes()[0]
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	original := reapChild
	reapChild = child.reap
	t.Cleanup(func() { reapChild = original })

	s.manager.StopAll()
	return s, p
}

func TestWaitForShutdownReapsExitedChild(t *testing.T) {
	child := &fakeChild{pid: 4242}
	s, p := shutdownFixture(t, child, 10)

	start := time.Now()
	s.waitForShutdown()
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Fatalf("waitForShutdown took %v; the exited child was not reaped", elapsed)
	}
	if p.State() != process.Stopped {
		t.Fatalf("state = %v, want STOPPED", p.State())
	}
	if child.gotKill() {
		t.Fatal("child that exited on SIGTERM was sent SIGKILL")
	}
}

func TestWaitForShutdownHandlesSigchld(t *testing.T) {
	child := &fakeChild{pid: 4243}
	s, p := shutdownFixture(t, child, 10)

	ch := make(chan os.Signal, 1)
	ch <- syscall.SIGCHLD
	s.signals = &SignalQueue{C: ch, ch: ch, logger: discardLogger()}

	s.waitForShutdown()

	if p.State() != process.Stopped {
		t.Fatalf("state = %v, want STOPPED", p.State())
	}
}

func TestWaitForShutdownSecondSignalKills(t *testing.T) {
	child := &fakeChild{pid: 4244, ignoreTE: true}
	s, p := shutdownFixture(t, child, 10)

	ch := make(chan os.Signal, 1)
	ch <- syscall.SIGTERM
	s.signals = &SignalQueue{C: ch, ch: ch, logger: discardLogger()}

	start := time.Now()
	s.waitForShutdown()
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("second SIGTERM did not end shutdown promptly: %v", elapsed)
	}
	if !child.gotKill() {
		t.Fatal("second SIGTERM did not SIGKILL the remaining process")
	}
	if p.State() != process.Stopped {
		t.Fatalf("state = %v, want STOPPED", p.State())
	}
}

func TestWaitForShutdownTimeoutKillsRemaining(t *testing.T) {
	child := &fakeChild{pid: 4245, ignoreTE: true}
	s, p := shutdownFixture(t, child, 1)

	start := time.Now()
	s.waitForShutdown()
	elapsed := time.Since(start)

	if elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("waitForShutdown took %v, want about the 1s shutdown_timeout", elapsed)
	}
	if !child.gotKill() {
		t.Fatal("shutdown timeout did not SIGKILL the remaining process")
	}
	if p.State() != process.Stopped {
		t.Fatalf("state = %v, want STOPPED", p.State())
	}
}
