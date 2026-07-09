package process

import (
	"os"
	"testing"
)

func TestExecSpawnerSpawn(t *testing.T) {
	s := &ExecSpawner{}
	sp, err := s.Spawn(SpawnConfig{
		Command: "/bin/echo",
		Args:    []string{"hello"},
		Umask:   -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sp.Pid() <= 0 {
		t.Fatalf("pid = %d, want > 0", sp.Pid())
	}

	// Read stdout.
	buf := make([]byte, 64)
	n, _ := sp.StdoutPipe().Read(buf)
	if n == 0 {
		t.Fatal("expected stdout output")
	}

	// Wait for exit.
	state, err := sp.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Success() {
		t.Fatalf("exit code = %d, want 0", state.ExitCode())
	}
}

func TestExecSpawnerSpawnInvalidCommand(t *testing.T) {
	s := &ExecSpawner{}
	_, err := s.Spawn(SpawnConfig{
		Command: "/nonexistent/binary",
		Umask:   -1,
	})
	if err == nil {
		t.Fatal("expected error for invalid command")
	}
}

func TestExecSpawnerSignal(t *testing.T) {
	s := &ExecSpawner{}
	sp, err := s.Spawn(SpawnConfig{
		Command: "/bin/sleep",
		Args:    []string{"10"},
		Umask:   -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = sp.Wait() }()

	if err := sp.Signal(os.Kill); err != nil {
		t.Fatal(err)
	}
}

func TestExecSpawnerStdinPipe(t *testing.T) {
	s := &ExecSpawner{}
	sp, err := s.Spawn(SpawnConfig{
		Command: "/bin/cat",
		Umask:   -1,
	})
	if err != nil {
		t.Fatal(err)
	}

	pipe := sp.StdinPipe()
	if pipe == nil {
		t.Fatal("expected non-nil stdin pipe")
	}
	pipe.Close()
	_, _ = sp.Wait()
}

func TestMockSpawnerDefault(t *testing.T) {
	ms := &MockSpawner{}
	sp, err := ms.Spawn(SpawnConfig{Command: "/bin/echo"})
	if err != nil {
		t.Fatal(err)
	}
	if sp.Pid() <= 0 {
		t.Fatalf("pid = %d, want > 0", sp.Pid())
	}
	if len(ms.SpawnCalls) != 1 {
		t.Fatalf("SpawnCalls = %d, want 1", len(ms.SpawnCalls))
	}
}

func TestMockProcessAccessors(t *testing.T) {
	mp := NewMockProcess(1234)
	if mp.Pid() != 1234 {
		t.Fatalf("pid = %d, want 1234", mp.Pid())
	}
	if mp.StdinPipe() == nil {
		t.Fatal("expected non-nil stdin")
	}
	if mp.StdoutPipe() == nil {
		t.Fatal("expected non-nil stdout")
	}
	if mp.StderrPipe() == nil {
		t.Fatal("expected non-nil stderr")
	}

	// Signal with no signalFn should return nil.
	if err := mp.Signal(os.Kill); err != nil {
		t.Fatal(err)
	}
}

func TestMockProcessCustomSignal(t *testing.T) {
	mp := NewMockProcess(1234)
	var received os.Signal
	mp.signalFn = func(sig os.Signal) error {
		received = sig
		return nil
	}

	if err := mp.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if received != os.Interrupt {
		t.Fatalf("signal = %v, want interrupt", received)
	}
}

func TestMockProcessCustomWait(t *testing.T) {
	mp := NewMockProcess(1234)
	mp.waitFn = func() (*os.ProcessState, error) {
		return nil, nil
	}

	state, err := mp.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if state != nil {
		t.Fatal("expected nil state")
	}
}

func TestMockPipeWriter(t *testing.T) {
	w := &mockPipeWriter{}
	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("n = %d, want 5", n)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !w.closed {
		t.Fatal("expected closed=true")
	}
}

func TestMockPipeReader(t *testing.T) {
	r := &mockPipeReader{}
	buf := make([]byte, 10)
	_, err := r.Read(buf)
	if err == nil {
		t.Fatal("expected EOF")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if !r.closed {
		t.Fatal("expected closed=true")
	}
}
