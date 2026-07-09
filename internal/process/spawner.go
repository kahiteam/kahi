package process

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// SpawnConfig holds the parameters needed to spawn a child process.
type SpawnConfig struct {
	Command     string               // absolute path or $PATH-resolved binary
	Args        []string             // command arguments (not including argv[0])
	Dir         string               // working directory
	Env         []string             // environment variables (KEY=VALUE)
	Stdout      io.Writer            // stdout destination (nil = discard)
	Stderr      io.Writer            // stderr destination (nil = discard)
	Stdin       io.Reader            // stdin source (nil = /dev/null)
	Umask       int                  // process umask (-1 means inherit)
	User        string               // uid:gid for credential switching
	RLimits     []RLimit             // resource limits to apply
	ExtraFiles  []*os.File           // additional file descriptors to pass
	SysProcAttr *syscall.SysProcAttr // additional proc attributes
}

// RLimit represents a resource limit to apply to a child process.
type RLimit struct {
	Resource int    // syscall.RLIMIT_* constant
	Cur      uint64 // soft limit
	Max      uint64 // hard limit
}

// SpawnedProcess represents a running child process.
type SpawnedProcess interface {
	Pid() int
	Wait() (*os.ProcessState, error)
	Signal(os.Signal) error
	StdinPipe() io.WriteCloser
	StdoutPipe() io.ReadCloser
	StderrPipe() io.ReadCloser
}

// ProcessSpawner creates child processes. Implementations include
// ExecSpawner (real) and MockSpawner (testing).
type ProcessSpawner interface {
	Spawn(cfg SpawnConfig) (SpawnedProcess, error)
}

// ExecSpawner spawns real OS processes via os/exec.
type ExecSpawner struct{}

type execProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

// Spawn starts a real child process with the given config.
func (s *ExecSpawner) Spawn(cfg SpawnConfig) (SpawnedProcess, error) {
	command, args := cfg.Command, cfg.Args

	// Resource limits and umask cannot be set via SysProcAttr, and applying
	// them in the supervisor before fork would mutate the supervisor itself.
	// When either is configured, re-exec the supervisor as a spawn trampoline
	// that applies them in the child (post-fork, pre-exec) and then execs the
	// real target. Credential switching still travels via SysProcAttr below.
	if len(cfg.RLimits) > 0 || cfg.Umask >= 0 {
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("cannot locate executable for spawn helper: %w", err)
		}
		command = self
		args = append([]string{
			ChildInitArg,
			strconv.Itoa(cfg.Umask),
			encodeRLimits(cfg.RLimits),
			"--",
			cfg.Command,
		}, cfg.Args...)
	}

	cmd := exec.Command(command, args...)
	cmd.Dir = cfg.Dir

	if cfg.Env != nil {
		cmd.Env = cfg.Env
	}

	// Set process group for isolation.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	if cfg.SysProcAttr != nil {
		cmd.SysProcAttr = cfg.SysProcAttr
	}
	cmd.SysProcAttr.Setpgid = true

	cmd.ExtraFiles = cfg.ExtraFiles

	// Set up pipes.
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &execProcess{
		cmd:    cmd,
		stdin:  stdinPipe,
		stdout: stdoutPipe,
		stderr: stderrPipe,
	}, nil
}

func (p *execProcess) Pid() int                        { return p.cmd.Process.Pid }
func (p *execProcess) Wait() (*os.ProcessState, error) { return p.cmd.Process.Wait() }
func (p *execProcess) Signal(sig os.Signal) error      { return p.cmd.Process.Signal(sig) }
func (p *execProcess) StdinPipe() io.WriteCloser       { return p.stdin }
func (p *execProcess) StdoutPipe() io.ReadCloser       { return p.stdout }
func (p *execProcess) StderrPipe() io.ReadCloser       { return p.stderr }

// MockSpawner is a test double for ProcessSpawner.
type MockSpawner struct {
	SpawnFn    func(cfg SpawnConfig) (SpawnedProcess, error)
	SpawnCalls []SpawnConfig
}

// Spawn records the call and delegates to SpawnFn.
func (m *MockSpawner) Spawn(cfg SpawnConfig) (SpawnedProcess, error) {
	m.SpawnCalls = append(m.SpawnCalls, cfg)
	if m.SpawnFn != nil {
		return m.SpawnFn(cfg)
	}
	return &MockProcess{pid: 1000 + len(m.SpawnCalls)}, nil
}

// MockProcess is a test double for SpawnedProcess.
type MockProcess struct {
	pid      int
	waitFn   func() (*os.ProcessState, error)
	signalFn func(os.Signal) error
	stdin    *mockPipeWriter
	stdout   io.ReadCloser
	stderr   io.ReadCloser
}

// NewMockProcess creates a MockProcess with the given PID.
func NewMockProcess(pid int) *MockProcess {
	return &MockProcess{
		pid:    pid,
		stdin:  &mockPipeWriter{},
		stdout: &mockPipeReader{},
		stderr: &mockPipeReader{},
	}
}

func (p *MockProcess) Pid() int { return p.pid }

func (p *MockProcess) Wait() (*os.ProcessState, error) {
	if p.waitFn != nil {
		return p.waitFn()
	}
	// Block forever by default.
	select {}
}

func (p *MockProcess) Signal(sig os.Signal) error {
	if p.signalFn != nil {
		return p.signalFn(sig)
	}
	return nil
}

func (p *MockProcess) StdinPipe() io.WriteCloser { return p.stdin }
func (p *MockProcess) StdoutPipe() io.ReadCloser { return p.stdout }
func (p *MockProcess) StderrPipe() io.ReadCloser { return p.stderr }

type mockPipeWriter struct{ closed bool }

func (w *mockPipeWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *mockPipeWriter) Close() error                { w.closed = true; return nil }

type mockPipeReader struct{ closed bool }

func (r *mockPipeReader) Read(p []byte) (int, error) { return 0, io.EOF }
func (r *mockPipeReader) Close() error               { r.closed = true; return nil }
