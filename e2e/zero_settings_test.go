//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An explicit 0 in the config means 0, not "use the default" (#138).

// countLines returns the number of lines in path, or 0 if it is missing.
func countLines(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n")
}

// startretries = 0: a program that fails to start goes FATAL after its
// first attempt; the default of 3 would retry it.
func TestZero_StartretriesNoRetry(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := writeScript(t, dir, "fail.sh", "echo run >> "+runs+"\nexit 1")
	client, _ := startDaemon(t, `
[programs.flaky]
command = "`+script+`"
startsecs = 1
startretries = 0
autorestart = "false"
`)
	waitForState(t, client, "flaky", "FATAL", 10*time.Second)
	// Give a retry, had there been one, time to happen.
	time.Sleep(1500 * time.Millisecond)
	if n := countLines(runs); n != 1 {
		t.Fatalf("program ran %d times, want 1 (startretries = 0)", n)
	}
}

// exitcodes = []: no exit status is expected, so with autorestart =
// "unexpected" even exit 0 restarts the program; the default [0] would not.
func TestZero_EmptyExitcodesRestartOnAnyExit(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := writeScript(t, dir, "once.sh", "echo run >> "+runs+"\nsleep 1\nexit 0")
	client, _ := startDaemon(t, `
[programs.oneshot]
command = "`+script+`"
startsecs = 0
autorestart = "unexpected"
exitcodes = []
`)
	waitForState(t, client, "oneshot", "RUNNING", 5*time.Second)
	deadline := time.Now().Add(10 * time.Second)
	for countLines(runs) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("program ran %d times in 10s, want a restart after exit 0 (exitcodes = [])", countLines(runs))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// *_logfile_backups = 0: rotation empties the log in place and keeps no
// backup files.
func TestZero_LogfileBackupsKeepsNone(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "out.log")
	script := writeScript(t, dir, "chatty.sh", "while true; do echo 0123456789012345678901234567890123456789; sleep 0.02; done")
	client, _ := startDaemon(t, `
[programs.chatty]
command = "`+script+`"
startsecs = 0
stopwaitsecs = 2
stdout_logfile = "`+logPath+`"
stdout_logfile_maxbytes = "1KB"
stdout_logfile_backups = 0
`)
	waitForState(t, client, "chatty", "RUNNING", 5*time.Second)
	// About 2 KB per second: enough for several rotations.
	time.Sleep(3 * time.Second)

	matches, err := filepath.Glob(logPath + ".*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("backup files %v exist, want none with stdout_logfile_backups = 0", matches)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	// One line can land after the size check, so allow a little over 1 KB.
	if info.Size() > 1024+128 {
		t.Fatalf("%s is %d bytes after 3s of output, want it kept under ~1 KB by rotation", logPath, info.Size())
	}
}

// Zero where it is unsafe is a config error: the daemon does not start.
func TestZero_UnsafeValuesRefused(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
	}{
		{
			name:   "stopwaitsecs 0",
			config: "[programs.web]\ncommand = \"/bin/sleep 300\"\nstopwaitsecs = 0\n",
			want:   `programs.web: stopwaitsecs must be >= 1, got 0; use stopsignal = "KILL" for an immediate kill`,
		},
		{
			name:   "shutdown_timeout 0",
			config: "[supervisor]\nshutdown_timeout = 0\n\n[programs.web]\ncommand = \"/bin/sleep 300\"\n",
			want:   "supervisor.shutdown_timeout must be >= 1, got 0",
		},
		{
			name:   "numprocs 0",
			config: "[programs.web]\ncommand = \"/bin/sleep 300\"\nnumprocs = 0\n",
			want:   "programs.web: numprocs must be >= 1, got 0",
		},
		{
			name:   "negative startretries",
			config: "[programs.web]\ncommand = \"/bin/sleep 300\"\nstartretries = -1\n",
			want:   "programs.web: startretries must be >= 0, got -1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := shortTempDir(t)
			configPath := filepath.Join(dir, "kahi.toml")
			// The [server.unix] table goes last so it cannot swallow the
			// test's own top-level tables.
			cfg := fmt.Sprintf("%s\n[server.unix]\nfile = %q\n", tc.config, filepath.Join(dir, "kahi.sock"))
			if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := runKahi(t, dir, "daemon", "-c", configPath)
			if code == 0 {
				t.Fatalf("daemon exited 0, want a config error\nstdout: %s\nstderr: %s", stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
		})
	}
}
