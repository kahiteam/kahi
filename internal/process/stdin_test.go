package process

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestWriteStdinBrokenPipe: a write to a closed stdin pipe is reported as
// "stdin pipe broken for {name}" with the cause wrapped and logged (#259).
func TestWriteStdinBrokenPipe(t *testing.T) {
	for _, cause := range []error{syscall.EPIPE, os.ErrClosed, io.ErrClosedPipe} {
		t.Run(cause.Error(), func(t *testing.T) {
			mp := NewMockProcess(1234)
			mp.stdin.err = &os.PathError{Op: "write", Path: "|1", Err: cause}
			spawner := &MockSpawner{SpawnFn: func(SpawnConfig) (SpawnedProcess, error) { return mp, nil }}
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, nil))
			p := NewProcess("web", "web", defaultProgramConfig(), spawner, testBus(), logger)
			if err := p.Start(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)

			err := p.WriteStdin([]byte("x\n"))
			if err == nil || err.Error() != "stdin pipe broken for web" {
				t.Fatalf("err = %v, want 'stdin pipe broken for web'", err)
			}
			if !errors.Is(err, cause) {
				t.Errorf("err does not wrap %v", cause)
			}
			if out := logBuf.String(); !strings.Contains(out, "stdin pipe broken for web") || !strings.Contains(out, cause.Error()) {
				t.Errorf("log %q lacks the message and cause %q", out, cause)
			}
		})
	}
}

// TestWriteStdinOtherError: an error that is not a closed pipe passes through.
func TestWriteStdinOtherError(t *testing.T) {
	mp := NewMockProcess(1234)
	mp.stdin.err = errors.New("disk on fire")
	spawner := &MockSpawner{SpawnFn: func(SpawnConfig) (SpawnedProcess, error) { return mp, nil }}
	p := NewProcess("web", "web", defaultProgramConfig(), spawner, testBus(), testLogger())
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := p.WriteStdin([]byte("x")); err == nil || err.Error() != "disk on fire" {
		t.Fatalf("err = %v, want 'disk on fire'", err)
	}
}
