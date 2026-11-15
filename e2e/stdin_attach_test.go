//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStdin_Write(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "cat.sh", "while read line; do echo got:$line; done")

	client, _ := startDaemon(t, `
[programs.cat]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "cat", "RUNNING", 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	if err := client.WriteStdin("cat", "hello"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("cat", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !strings.Contains(buf.String(), "got:hello") {
		t.Fatalf("output = %q, want 'got:hello'", buf.String())
	}
}

func TestStdin_WriteStopped(t *testing.T) {
	t.Parallel()
	client, _ := startDaemon(t, `
[programs.stopped]
command = "/bin/cat"
autostart = false
`)
	err := client.WriteStdin("stopped", "data")
	if err == nil {
		t.Fatal("expected error writing stdin to stopped process")
	}
}

func TestStdin_Attach(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "interactive.sh", "while read line; do echo reply:$line; done")

	client, _ := startDaemon(t, `
[programs.interactive]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "interactive", "RUNNING", 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var stdout struct {
		mu  sync.Mutex
		buf bytes.Buffer
	}

	// Use a pipe so we control when stdin data is written.
	// This avoids a race where the StringReader is consumed before
	// TailFollow establishes the SSE connection.
	pr, pw := io.Pipe()

	go func() {
		_ = client.Attach(ctx, "interactive", pr, &writerFunc{func(p []byte) (int, error) {
			stdout.mu.Lock()
			defer stdout.mu.Unlock()
			return stdout.buf.Write(p)
		}})
	}()

	// Give TailFollow time to establish the SSE connection.
	time.Sleep(1 * time.Second)

	// Now write stdin data through the pipe.
	_, _ = pw.Write([]byte("test-input\n"))

	deadline := time.After(8 * time.Second)
	for {
		stdout.mu.Lock()
		got := stdout.buf.String()
		stdout.mu.Unlock()
		if strings.Contains(got, "reply:test-input") {
			pw.Close()
			return
		}
		select {
		case <-deadline:
			stdout.mu.Lock()
			t.Fatalf("no reply received, stdout = %q", stdout.buf.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// writerFunc adapts a function to io.Writer.
type writerFunc struct {
	fn func([]byte) (int, error)
}

func (w *writerFunc) Write(p []byte) (int, error) {
	return w.fn(p)
}

// Data with quotes and backslashes must reach the process unchanged (#112).
func TestStdin_WriteSpecialCharacters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "cat.sh", "while IFS= read -r line; do printf 'got:%s\\n' \"$line\"; done")

	client, _ := startDaemon(t, `
[programs.cat]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "cat", "RUNNING", 5*time.Second)
	time.Sleep(500 * time.Millisecond)

	data := `say "hi" \ back`
	if err := client.WriteStdin("cat", data); err != nil {
		t.Fatalf("write stdin: %v", err)
	}

	want := "got:" + data
	deadline := time.Now().Add(5 * time.Second)
	for {
		var buf bytes.Buffer
		if err := client.Tail("cat", "stdout", 4096, &buf); err == nil && strings.Contains(buf.String(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("output never contained %q", want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestStdin_ErrorsAndBrokenPipe proves the stdin error paths of FUNC-077
// (#259): a stopped process is "process not running" (400), a running
// process whose stdin is closed is "stdin pipe broken for {name}" (409),
// kahi ctl send exits 1 with the same text, and the daemon log keeps the
// underlying cause.
func TestStdin_ErrorsAndBrokenPipe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	closer := writeScript(t, dir, "closer.sh", "exec 0<&-\necho closed\nexec sleep 30")
	client, socket, out := startDaemonOut(t, `
[programs.stopped]
command = "/bin/cat"
autostart = false

[programs.broken]
command = "`+closer+`"
autostart = true
startsecs = 0
`)

	apiDo(t, socket, "POST", "/api/v1/processes/stopped/stdin", `{"data":"x"}`).
		wantError(t, "stdin to stopped", http.StatusBadRequest, "process not running: stopped", "BAD_REQUEST")
	if _, stderr, code := runKahi(t, dir, "ctl", "-s", socket, "send", "stopped", "x"); code != 1 || !strings.Contains(stderr, "process not running: stopped") {
		t.Errorf("ctl send to stopped: exit %d, stderr %q; want 1 and 'process not running: stopped'", code, stderr)
	}

	// The program closes fd 0 before printing "closed", so once "closed" is
	// in its output a write meets a closed pipe.
	waitForState(t, client, "broken", "RUNNING", 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var buf bytes.Buffer
		if err := client.Tail("broken", "stdout", 4096, &buf); err == nil && strings.Contains(buf.String(), "closed") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("program never printed 'closed'")
		}
		time.Sleep(50 * time.Millisecond)
	}

	apiDo(t, socket, "POST", "/api/v1/processes/broken/stdin", `{"data":"x\n"}`).
		wantError(t, "stdin to closed pipe", http.StatusConflict, "stdin pipe broken for broken", "CONFLICT")
	if _, stderr, code := runKahi(t, dir, "ctl", "-s", socket, "send", "broken", "x"); code != 1 || !strings.Contains(stderr, "stdin pipe broken for broken") {
		t.Errorf("ctl send to closed pipe: exit %d, stderr %q; want 1 and 'stdin pipe broken for broken'", code, stderr)
	}

	// The cause (EPIPE) stays in the daemon log next to the message.
	deadline = time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(out.Name())
		found := false
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "stdin pipe broken for broken") && strings.Contains(line, "broken pipe") {
				found = true
				break
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon log has no line with 'stdin pipe broken for broken' and 'broken pipe':\n%s", b)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
