//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// Process output without a log file reaches the daemon's own stdout and
// stderr as JSON lines, where a container runtime collects it (FUNC-018,
// #137).

// lockedBuffer is a bytes.Buffer safe for the exec copy goroutine and the
// test reading it at the same time.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type outputLine struct {
	Time    string `json:"time"`
	Process string `json:"process"`
	Stream  string `json:"stream"`
	Log     string `json:"log"`
}

// processLines returns the JSON output lines in out that belong to process,
// skipping the daemon's own log lines.
func processLines(t *testing.T, out, process string) []outputLine {
	t.Helper()
	var lines []outputLine
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var l outputLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Process != process {
			continue
		}
		if _, err := time.Parse(time.RFC3339, l.Time); err != nil {
			t.Fatalf("line %q: time %q is not RFC3339: %v", sc.Text(), l.Time, err)
		}
		lines = append(lines, l)
	}
	return lines
}

func logsOf(lines []outputLine, stream string) []string {
	var logs []string
	for _, l := range lines {
		if l.Stream == stream {
			logs = append(logs, l.Log)
		}
	}
	return logs
}

// startDaemonCaptured starts the daemon in dir with its stdout and stderr
// captured, waits for every program to exit, and returns the captured output
// once the daemon has stopped. DIR in programs is replaced with dir.
func startDaemonCaptured(t *testing.T, dir string, programs string) (stdout, stderr string) {
	t.Helper()
	socketPath := filepath.Join(dir, "kahi.sock")
	configPath := filepath.Join(dir, "kahi.toml")
	cfg := fmt.Sprintf("[supervisor]\nlog_level = \"warn\"\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n\n%s", socketPath, strings.ReplaceAll(programs, "DIR", dir))
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	var out, errb lockedBuffer
	cmd := exec.Command(kahiBinary, "daemon", "-c", configPath)
	cmd.Dir = dir
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForSocket(t, socketPath, 5*time.Second)
	client := ctl.NewUnixClient(socketPath)
	waitForHealth(t, client, 3*time.Second)
	// Each program prints, then exits.
	deadline := time.Now().Add(10 * time.Second)
	for {
		infos, err := getAllProcessInfo(client)
		done := err == nil && len(infos) > 0
		for _, info := range infos {
			if info.State != "EXITED" {
				done = false
			}
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("programs did not all exit: %+v (err %v)\nstderr:\n%s", infos, err, errb.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = client.Shutdown()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-waitDone
		t.Fatal("daemon did not exit after shutdown")
	}
	return out.String(), errb.String()
}

func TestOutput_PassthroughJSONLines(t *testing.T) {
	dir := shortTempDir(t)
	{
		writeScript(t, dir, "print.sh", "printf 'first \\033[31mred\\033[0m\\n'\necho oops >&2\necho second\nprintf 'no newline'\nsleep 1.5")
	}
	stdout, stderr := startDaemonCaptured(t, dir, `
[programs.plain]
command = "DIR/print.sh"
autorestart = "false"
startsecs = 0
strip_ansi = true

[programs.merged]
command = "DIR/print.sh"
autorestart = "false"
startsecs = 0
redirect_stderr = true

[programs.tofile]
command = "DIR/print.sh"
autorestart = "false"
startsecs = 0
stdout_logfile = "DIR/tofile.log"
stderr_logfile = "DIR/tofile.err"
`)

	// stdout lines go to the daemon's stdout, ANSI stripped; a final line
	// without a newline is flushed when the pipe closes.
	plain := processLines(t, stdout, "plain")
	if got, want := logsOf(plain, "stdout"), []string{"first red", "second", "no newline"}; !equal(got, want) {
		t.Errorf("plain stdout lines = %q, want %q\nstdout:\n%s", got, want, stdout)
	}
	if got := logsOf(plain, "stderr"); len(got) != 0 {
		t.Errorf("plain stderr lines on stdout: %q", got)
	}
	if got, want := logsOf(processLines(t, stderr, "plain"), "stderr"), []string{"oops"}; !equal(got, want) {
		t.Errorf("plain stderr lines on stderr = %q, want %q\nstderr:\n%s", got, want, stderr)
	}

	// redirect_stderr merges stderr into the stdout stream.
	merged := processLines(t, stdout, "merged")
	if got := logsOf(merged, "stdout"); !contains(got, "oops") || !contains(got, "second") {
		t.Errorf("merged stdout lines = %q, want stdout and stderr output", got)
	}
	if got := processLines(t, stderr, "merged"); len(got) != 0 {
		t.Errorf("merged lines on stderr: %+v", got)
	}

	// Streams with a log file go to the file, raw, and are not forwarded.
	if got := processLines(t, stdout+stderr, "tofile"); len(got) != 0 {
		t.Errorf("tofile lines forwarded despite log files: %+v", got)
	}
	for file, want := range map[string]string{"tofile.log": "second\n", "tofile.err": "oops\n"} {
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s = %q (err %v), want it to contain %q", file, b, err, want)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
