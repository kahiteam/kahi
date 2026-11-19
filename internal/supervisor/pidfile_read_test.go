package supervisor

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A PID path the drop user replaced with a symlink to a secret, or a FIFO,
// is never read: the start does not log the secret or block, and the stale
// path is replaced (SEC-019's principle for the PID file).
func TestPIDFileNotFollowed(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "shadow")
	if err := os.WriteFile(secret, []byte("root:$6$secrethash"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.pid")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo.pid")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, fifo} {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		done := make(chan error, 2)
		go func() { done <- CheckPIDFile(path, logger) }()
		go func() { _, err := ProbePIDFile(path, logger); done <- err }()
		for range 2 {
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("%s: %v", filepath.Base(path), err)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("%s: reading the PID file blocked", filepath.Base(path))
			}
		}
		if strings.Contains(buf.String(), "secrethash") || !strings.Contains(buf.String(), `content="(not a regular file)"`) {
			t.Errorf("%s: log %q", filepath.Base(path), buf.String())
		}
	}
}

// At most maxPIDFileRead bytes of a regular PID file are read and logged.
func TestPIDFileReadBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.pid")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 10000)), 0o644); err != nil {
		t.Fatal(err)
	}
	content, exists, err := readPIDFile(path)
	if err != nil || !exists || len(content) != maxPIDFileRead {
		t.Errorf("content %d bytes, exists %v, err %v", len(content), exists, err)
	}
}
