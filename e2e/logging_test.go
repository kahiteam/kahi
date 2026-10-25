//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestLog_TailStdout(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "echoer.sh", "echo hello-stdout\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.echoer]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "echoer", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("echoer", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail stdout: %v", err)
	}
	if !strings.Contains(buf.String(), "hello-stdout") {
		t.Fatalf("tail output = %q, want 'hello-stdout'", buf.String())
	}
}

func TestLog_TailStderr(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "errwriter.sh", "echo hello-stderr >&2\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.errwriter]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "errwriter", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("errwriter", "stderr", 4096, &buf); err != nil {
		t.Fatalf("tail stderr: %v", err)
	}
	if !strings.Contains(buf.String(), "hello-stderr") {
		t.Fatalf("tail output = %q, want 'hello-stderr'", buf.String())
	}
}

func TestLog_TailBytes(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "longout.sh", "seq 1 100 | while read i; do echo line-$i; done\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.longout]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "longout", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("longout", "stdout", 50, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if buf.Len() > 100 {
		t.Fatalf("tail returned %d bytes, expected roughly <= 50", buf.Len())
	}
}

// syncBuf wraps bytes.Buffer with a mutex for concurrent access.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestLog_TailFollow(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "streamer.sh", "while true; do echo stream-line; sleep 0.1; done")

	client, _ := startDaemon(t, `
[programs.streamer]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "streamer", "RUNNING", 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out syncBuf
	go func() {
		_ = client.TailFollow(ctx, "streamer", "stdout", &out)
	}()

	deadline := time.After(5 * time.Second)
	for {
		if strings.Contains(out.String(), "stream-line") {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("no output received from tail follow: %q", out.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func TestLog_Rotation(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	script := writeScript(t, dir, "bigwriter.sh", "while true; do dd if=/dev/zero bs=1024 count=1 2>/dev/null | tr '\\0' A; done")

	client, _ := startDaemon(t, `
[programs.bigwriter]
command = "`+script+`"
autostart = true
startsecs = 0
stdout_logfile = "`+logDir+`/stdout.log"
stdout_logfile_maxbytes = "10KB"
stdout_logfile_backups = 2
`)
	waitForState(t, client, "bigwriter", "RUNNING", 5*time.Second)

	deadline := time.After(15 * time.Second)
	for {
		matches, _ := filepath.Glob(filepath.Join(logDir, "stdout.log.*"))
		if len(matches) > 0 {
			t.Logf("rotation occurred, backup files: %v", matches)
			return
		}
		select {
		case <-deadline:
			t.Fatal("log rotation did not occur within 15 seconds")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func TestLog_ANSIStrip(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "colored.sh", `printf '\033[31mred text\033[0m\n'
exec sleep 300`)

	client, _ := startDaemon(t, `
[programs.colored]
command = "`+script+`"
autostart = true
startsecs = 0
strip_ansi = true
`)
	waitForState(t, client, "colored", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("colored", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	output := buf.String()
	if strings.Contains(output, "\033[") {
		t.Fatalf("ANSI escape sequences not stripped: %q", output)
	}
	if !strings.Contains(output, "red text") {
		t.Fatalf("text content missing: %q", output)
	}
}

// logrotate renames the live log file and sends SIGUSR2; the daemon must
// reopen the file at its configured path so new output lands there and the
// rotated file stops growing (#107).
func TestLog_SIGUSR2ReopensRotatedLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "out.log")
	script := writeScript(t, dir, "ticker.sh", "while :; do echo tick; sleep 0.1; done")

	client, _ := startDaemon(t, `
[programs.ticker]
command = "`+script+`"
autostart = true
startsecs = 0
stopwaitsecs = 2
stdout_logfile = "`+logPath+`"
`)
	waitForState(t, client, "ticker", "RUNNING", 5*time.Second)
	time.Sleep(300 * time.Millisecond)

	rotated := logPath + ".1"
	if err := os.Rename(logPath, rotated); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	pid, err := client.PID("")
	if err != nil {
		t.Fatalf("daemon pid: %v", err)
	}
	daemon, err := strconv.Atoi(strings.TrimSpace(pid))
	if err != nil {
		t.Fatalf("daemon pid %q: %v", pid, err)
	}
	if err := syscall.Kill(daemon, syscall.SIGUSR2); err != nil {
		t.Fatalf("SIGUSR2: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(logPath); err == nil && strings.Contains(string(b), "tick") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no output in the reopened log file 5s after SIGUSR2")
		}
		time.Sleep(50 * time.Millisecond)
	}

	before, _ := os.Stat(rotated)
	time.Sleep(500 * time.Millisecond)
	after, _ := os.Stat(rotated)
	if after.Size() != before.Size() {
		t.Fatalf("rotated file still growing after SIGUSR2 (%d -> %d bytes)", before.Size(), after.Size())
	}
}
