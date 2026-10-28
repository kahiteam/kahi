//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// The daemon's own log (FUNC-017, #142).

// logDaemon starts a daemon whose [supervisor] table holds supervisor and
// whose stdout is captured. It returns a client, the socket path and stdout.
func logDaemon(t *testing.T, dir, supervisor, programs string) (*ctl.Client, string, *syncBuffer) {
	t.Helper()
	sock := filepath.Join(dir, "kahi.sock")
	cfg := filepath.Join(dir, "kahi.toml")
	writeLogConfig(t, cfg, sock, supervisor, programs)
	cmd := exec.Command(kahiBinary, "daemon", "-c", cfg)
	cmd.Dir = dir
	out := &syncBuffer{}
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ctl.NewUnixClient(sock).Shutdown()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	waitForSocket(t, sock, 5*time.Second)
	client := ctl.NewUnixClient(sock)
	waitForHealth(t, client, 3*time.Second)
	return client, sock, out
}

func writeLogConfig(t *testing.T, path, sock, supervisor, programs string) {
	t.Helper()
	cfg := fmt.Sprintf("[supervisor]\nshutdown_timeout = 5\n%s\n[server.unix]\nfile = %q\n\n%s", supervisor, sock, programs)
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitFor polls get until it contains want.
func waitFor(t *testing.T, what string, get func() string, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := get()
		if strings.Contains(s, want) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s lacks %q:\n%s", what, want, s)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A program that exits at once, so the daemon logs the debug line
// "reaped child".
const exitingProgram = `
[programs.blip]
command = "/bin/sleep 0"
startsecs = 0
autorestart = "false"
`

// By default the daemon logs JSON lines on stdout at info level: no debug
// lines.
func TestDaemonLog_DefaultJSONInfo(t *testing.T) {
	client, _, out := logDaemon(t, shortTempDir(t), "", exitingProgram)
	waitForState(t, client, "blip", "EXITED", 5*time.Second)
	s := waitFor(t, "stdout", out.String, `"msg":"supervisor running"`)
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not a JSON line: %q", line)
		}
		if m["level"] == "DEBUG" {
			t.Errorf("debug line at the default info level: %s", line)
		}
	}
}

// log_format = "text" and log_level = "debug": text lines, debug included.
func TestDaemonLog_TextDebug(t *testing.T) {
	client, _, out := logDaemon(t, shortTempDir(t), "log_format = \"text\"\nlog_level = \"debug\"\n", exitingProgram)
	waitForState(t, client, "blip", "EXITED", 5*time.Second)
	s := waitFor(t, "stdout", out.String, `level=INFO msg="supervisor running"`)
	waitFor(t, "stdout", out.String, `level=DEBUG msg="reaped child"`)
	if strings.Contains(s, `{"time"`) {
		t.Errorf("JSON line with log_format = \"text\":\n%s", s)
	}
}

// logfile receives the log instead of stdout, appended to what is there.
func TestDaemonLog_FileAppends(t *testing.T) {
	dir := shortTempDir(t)
	logfile := filepath.Join(dir, "kahi.log")
	if err := os.WriteFile(logfile, []byte("previous line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, out := logDaemon(t, dir, fmt.Sprintf("logfile = %q\n", logfile), "")
	s := waitFor(t, "log file", func() string { b, _ := os.ReadFile(logfile); return string(b) }, `"msg":"supervisor running"`)
	if !strings.HasPrefix(s, "previous line\n") {
		t.Errorf("log file was truncated:\n%s", s)
	}
	if strings.Contains(out.String(), "supervisor running") {
		t.Errorf("daemon log also on stdout with a logfile:\n%s", out.String())
	}
}

// An invalid level or format and an unwritable logfile stop the daemon.
func TestDaemonLog_ConfigErrors(t *testing.T) {
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	for _, tc := range []struct {
		name, supervisor, want string
	}{
		{"level", `log_level = "verbose"`, `supervisor.log_level: invalid log level: "verbose"`},
		{"format", `log_format = "xml"`, `supervisor.log_format: invalid log format: "xml"`},
		{"logfile", fmt.Sprintf("logfile = %q", filepath.Join(locked, "kahi.log")), "cannot open log file: " + filepath.Join(locked, "kahi.log") + ": "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "logfile" && os.Geteuid() == 0 {
				t.Skip("root ignores directory permissions")
			}
			dir := shortTempDir(t)
			cfg := filepath.Join(dir, "kahi.toml")
			writeLogConfig(t, cfg, filepath.Join(dir, "kahi.sock"), tc.supervisor+"\n", "")
			_, stderr, code := runKahi(t, dir, "daemon", "-c", cfg)
			if code == 0 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d, stderr %q; want %q", code, stderr, tc.want)
			}
		})
	}
}

// A log_level change takes effect on reload, without a restart.
func TestDaemonLog_ReloadChangesLevel(t *testing.T) {
	dir := shortTempDir(t)
	logfile := filepath.Join(dir, "kahi.log")
	read := func() string { b, _ := os.ReadFile(logfile); return string(b) }
	client, sock, _ := logDaemon(t, dir, fmt.Sprintf("logfile = %q\n", logfile), exitingProgram)
	waitForState(t, client, "blip", "EXITED", 5*time.Second)
	waitFor(t, "log", read, `"msg":"supervisor running"`)
	if strings.Contains(read(), `"msg":"reaped child"`) {
		t.Fatal("debug line at info level")
	}

	writeLogConfig(t, filepath.Join(dir, "kahi.toml"), sock, fmt.Sprintf("logfile = %q\nlog_level = \"debug\"\n", logfile), exitingProgram)
	if _, err := client.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	waitFor(t, "log", read, `"msg":"log level changed","from":"info","to":"debug"`)
	if err := client.Start("blip"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "log", read, `"msg":"reaped child"`)
}
