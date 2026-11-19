//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// kahi config check (FUNC-087, #272): reports whether kahi daemon with the
// same flags would start, without creating, binding or spawning anything.

// checkConfig writes body into a fresh short directory (socket at
// dir/kahi.sock unless body sets one) and returns the directory and the
// config path.
func checkConfig(t *testing.T, body string) (dir, path string) {
	t.Helper()
	dir = realDir(t)
	path = filepath.Join(dir, "kahi.toml")
	if !strings.Contains(body, "[server.unix]") {
		body = fmt.Sprintf("[server.unix]\nfile = %q\n\n%s", filepath.Join(dir, "kahi.sock"), body)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func skipCheckAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("runs as non-root: root ignores directory modes and prints the root warning")
	}
}

// requireDefaultLimits skips when the hard limits are below the defaults
// minfds 1024 and minprocs 200, which add a warning.
func requireDefaultLimits(t *testing.T) {
	t.Helper()
	var l unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &l); err != nil || l.Max < 1024 {
		t.Skip("NOFILE hard limit below 1024")
	}
	if err := unix.Getrlimit(unix.RLIMIT_NPROC, &l); err != nil || l.Max < 200 {
		t.Skip("NPROC hard limit below 200")
	}
}

func TestConfigCheck_OK(t *testing.T) {
	t.Parallel()
	skipCheckAsRoot(t)
	requireDefaultLimits(t)
	dir, path := checkConfig(t, "[programs.web]\ncommand = \"/bin/sleep 300\"\n")
	stdout, stderr, code := runKahi(t, dir, "config", "check", "-c", path)
	if code != 0 || stdout != "config OK: "+path+"\n" || stderr != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if _, err := os.Lstat(filepath.Join(dir, "kahi.sock")); err == nil {
		t.Error("the check created the socket")
	}
}

// A refused config gives the daemon's own text.
func TestConfigCheck_ConfigErrorMatchesDaemon(t *testing.T) {
	t.Parallel()
	dir, path := checkConfig(t, "[programs.web]\ncommand = \"/bin/sleep 300\"\nstopwaitsecs = 0\n")
	stdout, stderr, code := runKahi(t, dir, "config", "check", "-c", path)
	_, dstderr, dcode := runKahi(t, dir, "daemon", "-c", path)
	if code != 1 || stdout != "" {
		t.Fatalf("code %d, stdout %q", code, stdout)
	}
	if !strings.Contains(stderr, "config validation failed in "+path+":") || !strings.Contains(stderr, "programs.web: stopwaitsecs must be >= 1") {
		t.Errorf("stderr %q", stderr)
	}
	if dcode != 1 || dstderr != stderr {
		t.Errorf("daemon exit %d, stderr %q; check stderr %q", dcode, dstderr, stderr)
	}
}

func TestConfigCheck_Warnings(t *testing.T) {
	t.Parallel()
	dir, path := checkConfig(t, "[programs.web]\ncommand = \"/bin/sleep 300\"\nnosuchkey = 1\n")
	stdout, stderr, code := runKahi(t, dir, "config", "check", "-c", path)
	if code != 0 || stdout != "config OK: "+path+"\n" || !strings.Contains(stderr, "warning: unknown config key: programs.web.nosuchkey") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// Every startup probe runs and each failure is one line; nothing is created.
func TestConfigCheck_ProbeErrors(t *testing.T) {
	t.Parallel()
	base := realDir(t)
	logPath := filepath.Join(base, "nolog", "kahi.log")
	pidPath := filepath.Join(base, "nopid", "kahi.pid")
	dir, path := checkConfig(t, fmt.Sprintf("[supervisor]\ndirectory = %q\nlogfile = %q\n\n[programs.web]\ncommand = \"/bin/sleep 300\"\n",
		filepath.Join(base, "nodir"), logPath))
	stdout, stderr, code := runKahi(t, dir, "config", "check", "-c", path, "-p", pidPath)
	if code != 1 || stdout != "" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{
		"error: directory does not exist: " + filepath.Join(base, "nodir") + "\n",
		"error: cannot open log file: " + logPath + ": ",
		"error: cannot write PID file: " + pidPath + ": ",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if n := strings.Count(stderr, "error: "); n != 3 {
		t.Errorf("%d error lines, want 3:\n%s", n, stderr)
	}
	for _, p := range []string{filepath.Join(base, "nodir"), filepath.Dir(logPath), filepath.Dir(pidPath), filepath.Join(dir, "kahi.sock")} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("the check created %s", p)
		}
	}
}

// A log parent that is a symlink the checking user owns (the daemon's user)
// is followed, as the daemon follows it (SEC-019, decided 2026-11-19).
func TestConfigCheck_LogParentSymlink(t *testing.T) {
	t.Parallel()
	base := realDir(t)
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(base, "link", "kahi.log")
	dir, path := checkConfig(t, fmt.Sprintf("[supervisor]\nlogfile = %q\n", logPath))
	_, stderr, _ := runKahi(t, dir, "config", "check", "-c", path)
	if strings.Contains(stderr, "cannot open log file") {
		t.Errorf("check refused a link owned by the daemon's user: %q", stderr)
	}
}

func TestConfigCheck_MinfdsAboveHardLimit(t *testing.T) {
	t.Parallel()
	skipCheckAsRoot(t)
	dir, path := checkConfig(t, "[supervisor]\nminfds = 1024\n")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "ulimit -n 256 && exec \"$0\" config check -c \"$1\"", kahiBinary, path)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("err %v, stdout %q, stderr %q", err, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "error: cannot set NOFILE to 1024") {
		t.Errorf("stderr %q", errb.String())
	}
}

func TestConfigCheck_NoConfigFound(t *testing.T) {
	t.Parallel()
	requireNoSystemConfig(t)
	dir, _ := checkConfig(t, "[programs.web]\ncommand = \"/bin/sleep 300\"\n")
	stdout, stderr, code := runKahiEnv(t, dir, searchEnv(), "config", "check")
	if code != 1 || stdout != "" || stderr != searchedMsg {
		t.Fatalf("code %d, stdout %q, stderr %q (the current directory must not be searched)", code, stdout, stderr)
	}
}

// Program problems are warnings: the daemon would start.
func TestConfigCheck_ProgramWarnings(t *testing.T) {
	t.Parallel()
	dir, path := checkConfig(t, "[programs.web]\ncommand = \"/nonexistent/web\"\n\n[programs.job]\ncommand = \"/bin/sleep 300\"\ndirectory = \"/nonexistent/dir\"\n")
	stdout, stderr, code := runKahi(t, dir, "config", "check", "-c", path)
	if code != 0 || stdout != "config OK: "+path+"\n" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{
		"warning: programs.web.command: /nonexistent/web: no such file or directory\n",
		"warning: programs.job.directory: directory does not exist: /nonexistent/dir\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

// A running daemon is noted and left alone.
func TestConfigCheck_RunningDaemon(t *testing.T) {
	t.Parallel()
	dir, path := checkConfig(t, "[supervisor]\nshutdown_timeout = 10\n\n[programs.web]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\n")
	socket := filepath.Join(dir, "kahi.sock")
	pidPath := filepath.Join(dir, "kahi.pid")
	client, cmd, _ := rawDaemon(t, dir, socket, "", "-c", path, "-p", pidPath)
	daemonPID := cmd.Process.Pid

	stdout, stderr, code := runKahi(t, dir, "config", "check", "-c", path, "-p", pidPath)
	if code != 0 || stdout != "config OK: "+path+"\n" || !strings.Contains(stderr, fmt.Sprintf("note: a daemon is running (pid %d); checked as a fresh start", daemonPID)) {
		t.Fatalf("with -p: code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	stdout, stderr, code = runKahi(t, dir, "config", "check", "-c", path)
	if code != 0 || stdout != "config OK: "+path+"\n" || !strings.Contains(stderr, "note: a daemon is running (socket "+socket+"); checked as a fresh start") {
		t.Fatalf("without -p: code %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	if _, err := client.Health(); err != nil {
		t.Errorf("daemon not healthy after the checks: %v", err)
	}
	b, err := os.ReadFile(pidPath)
	if err != nil || strings.TrimSpace(string(b)) != strconv.Itoa(daemonPID) {
		t.Errorf("PID file %q, %v; want %d", b, err, daemonPID)
	}
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
}

func TestConfigCheck_DropUser(t *testing.T) {
	t.Parallel()
	skipCheckAsRoot(t)
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	dir, path := checkConfig(t, "[programs.web]\ncommand = \"/bin/sleep 300\"\n")
	stdout, stderr, code := runKahi(t, dir, "config", "check", "-c", path, "-u", me.Username)
	if code != 0 || stdout != "config OK: "+path+"\n" || !strings.Contains(stderr, "note: not root; the privilege drop was not checked\n") {
		t.Fatalf("-u %s: code %d, stdout %q, stderr %q", me.Username, code, stdout, stderr)
	}
	stdout, stderr, code = runKahi(t, dir, "config", "check", "-c", path, "-u", "nosuchuser")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "user not found: nosuchuser") {
		t.Fatalf("-u nosuchuser: code %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestConfigCheck_Usage(t *testing.T) {
	t.Parallel()
	dir, path := checkConfig(t, "")
	for _, args := range [][]string{
		{"config", "check", "-d", "-c", path},
		{"config", "check", "--bogus"},
		{"config", "check", "extra"},
		{"config", "nosuch"},
	} {
		if _, stderr, code := runKahi(t, dir, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2 (stderr %q)", args, code, stderr)
		}
	}
	stdout, _, code := runKahi(t, dir, "config")
	if code != 0 || !strings.Contains(stdout, "check") {
		t.Errorf("kahi config: exit %d, stdout %q", code, stdout)
	}
}

func TestConfigCheck_SocketPathNotSocket(t *testing.T) {
	t.Parallel()
	dir, path := checkConfig(t, "[programs.web]\ncommand = \"/bin/sleep 300\"\n")
	socket := filepath.Join(dir, "kahi.sock")
	if err := os.WriteFile(socket, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runKahi(t, dir, "config", "check", "-c", path)
	if code != 1 || !strings.Contains(stderr, "error: cannot create socket: "+socket+": "+socket+" exists and is not a socket") {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
	if b, err := os.ReadFile(socket); err != nil || string(b) != "keep" {
		t.Errorf("file changed: %q %v", b, err)
	}
}

// An [eventlisteners.x] mistake that stops the daemon stops the check with
// the same text; a pool whose command is missing is a warning, as for a
// program (FUNC-087, FUNC-055).
func TestConfigCheck_ListenerPools(t *testing.T) {
	t.Parallel()
	dir, path := checkConfig(t, "[eventlisteners.l]\nevents = [\"PROCESS_STATE_EXITED\"]\n")
	stdout, stderr, code := runKahi(t, dir, "config", "check", "-c", path)
	_, dstderr, dcode := runKahi(t, dir, "daemon", "-c", path)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "eventlisteners.l: command is required") {
		t.Errorf("missing command: %d %q %q", code, stdout, stderr)
	}
	if dcode != 1 || dstderr != stderr {
		t.Errorf("daemon %d %q, check %q", dcode, dstderr, stderr)
	}

	dir, path = checkConfig(t, "[eventlisteners.l]\ncommand = \"/nonexistent/lst\"\nevents = [\"PROCESS_STATE_EXITED\"]\n")
	stdout, stderr, code = runKahi(t, dir, "config", "check", "-c", path)
	if code != 0 || stdout != "config OK: "+path+"\n" || !strings.Contains(stderr, "warning: eventlisteners.l.command: /nonexistent/lst: no such file or directory") {
		t.Errorf("missing pool command: %d %q %q", code, stdout, stderr)
	}
}
