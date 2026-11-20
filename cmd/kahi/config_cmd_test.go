package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/supervisor"
	"github.com/kahiteam/kahi/internal/userspec"
)

// runCheck runs kahi config check in process with fresh flags.
func runCheck(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(t.TempDir()) // the check may chdir to [supervisor] directory
	checkConfigFlag, checkPidfileFlag, checkUserFlag = "", "", ""
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs(append([]string{"config", "check"}, args...))
	err = rootCmd.Execute()
	return out.String(), errb.String(), err
}

// shortTemp is a temp directory short enough for a Unix socket path.
func shortTemp(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "kc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func writeCheckConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "kahi.toml")
	if !strings.Contains(body, "[server.unix]") {
		body = fmt.Sprintf("[server.unix]\nfile = %q\n\n%s", filepath.Join(dir, "kahi.sock"), body)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfigCheckOK(t *testing.T) {
	dir := shortTemp(t)
	cfg := writeCheckConfig(t, dir, "[programs.web]\ncommand = \"/nonexistent/web\"\n")
	stdout, stderr, err := runCheck(t, "-c", cfg)
	if err != nil {
		t.Fatalf("err %v, stderr %q", err, stderr)
	}
	if stdout != "config OK: "+cfg+"\n" {
		t.Errorf("stdout %q", stdout)
	}
	if !strings.Contains(stderr, "warning: programs.web.command: /nonexistent/web: no such file or directory") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestConfigCheckProbeErrors(t *testing.T) {
	dir := shortTemp(t)
	cfg := writeCheckConfig(t, dir, fmt.Sprintf("[supervisor]\ndirectory = %q\nlogfile = %q\n",
		filepath.Join(dir, "nodir"), filepath.Join(dir, "nolog", "k.log")))
	pid := filepath.Join(dir, "nopid", "k.pid")
	stdout, stderr, err := runCheck(t, "-c", cfg, "-p", pid)
	var silent silentError
	if !errors.As(err, &silent) || exitCode(err) != 1 {
		t.Fatalf("err %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout %q", stdout)
	}
	for _, want := range []string{"error: directory does not exist: ", "error: cannot open log file: ", "error: cannot write PID file: "} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %s", want, stderr)
		}
	}
}

// A relative logfile is checked after the chdir to [supervisor] directory.
func TestConfigCheckRelativeAfterDirectory(t *testing.T) {
	dir := shortTemp(t)
	cfg := writeCheckConfig(t, dir, fmt.Sprintf("[supervisor]\ndirectory = %q\nlogfile = \"logs/k.log\"\n", dir))
	_, stderr, err := runCheck(t, "-c", cfg)
	if err == nil || !strings.Contains(stderr, "error: cannot open log file: logs/k.log: ") {
		t.Fatalf("err %v, stderr %q", err, stderr)
	}
	if err := os.Mkdir(filepath.Join(dir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := runCheck(t, "-c", cfg); err != nil {
		t.Fatalf("with logs/: err %v, stderr %q", err, stderr)
	}
}

func TestConfigCheckConfigError(t *testing.T) {
	dir := shortTemp(t)
	cfg := writeCheckConfig(t, dir, "[programs.web]\ncommand = \"sleep 1\"\nstopwaitsecs = 0\n")
	_, _, err := runCheck(t, "-c", cfg)
	if err == nil || !strings.Contains(err.Error(), "stopwaitsecs must be >= 1") {
		t.Fatalf("err %v", err)
	}
}

func TestConfigCheckRunningDaemon(t *testing.T) {
	dir := shortTemp(t)
	sock := filepath.Join(dir, "kahi.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	cfg := writeCheckConfig(t, dir, "")
	_, stderr, err := runCheck(t, "-c", cfg)
	if err != nil || !strings.Contains(stderr, "note: a daemon is running (socket "+sock+")") {
		t.Fatalf("socket: err %v, stderr %q", err, stderr)
	}

	pidPath := filepath.Join(dir, "k.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getppid())), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, err = runCheck(t, "-c", cfg, "-p", pidPath)
	if err != nil || !strings.Contains(stderr, fmt.Sprintf("note: a daemon is running (pid %d)", os.Getppid())) {
		t.Fatalf("pid: err %v, stderr %q", err, stderr)
	}
}

func TestConfigCheckSocketNotSocket(t *testing.T) {
	dir := shortTemp(t)
	if err := os.WriteFile(filepath.Join(dir, "kahi.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := writeCheckConfig(t, dir, "")
	_, stderr, err := runCheck(t, "-c", cfg)
	if err == nil || !strings.Contains(stderr, "exists and is not a socket") {
		t.Fatalf("err %v, stderr %q", err, stderr)
	}
}

func TestConfigCheckDropUser(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	dir := shortTemp(t)
	cfg := writeCheckConfig(t, dir, fmt.Sprintf("[supervisor]\nuser = %q\n", me.Username))
	_, stderr, err := runCheck(t, "-c", cfg, "-u", me.Username)
	if err != nil {
		t.Fatalf("err %v, stderr %q", err, stderr)
	}
	if os.Geteuid() != 0 && !strings.Contains(stderr, "note: not root; the privilege drop was not checked") {
		t.Errorf("stderr %q", stderr)
	}
	if !strings.Contains(stderr, "note: kahi daemon -u "+me.Username+" overrides supervisor.user") {
		t.Errorf("override note missing: %q", stderr)
	}
	if _, _, err := runCheck(t, "-c", cfg, "-u", "kahi-no-such-user-x"); err == nil {
		t.Error("unknown -u user must fail")
	}
}

func TestCheckHandler(t *testing.T) {
	var b bytes.Buffer
	l := slog.New(&checkHandler{w: &b}).With("a", 1).WithGroup("g")
	l.Warn("careful", "path", "/x")
	l.Info("fyi")
	want := "warning: careful a=1 path=/x\nnote: fyi a=1\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

// Values are quoted as slog's text handler quotes them.
func TestCheckHandlerQuotes(t *testing.T) {
	var b bytes.Buffer
	slog.New(&checkHandler{w: &b}).Warn("replacing stale PID file", "path", "/run/my dir/k.pid", "content", "12 34")
	if want := "warning: replacing stale PID file path=\"/run/my dir/k.pid\" content=\"12 34\"\n"; b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestProgramUsers(t *testing.T) {
	if u := programUsers(supervisor.DropCheck{}); u.Drop != nil || u.ProgramUser == nil {
		t.Errorf("no drop: %+v", u)
	}
	u := programUsers(supervisor.DropCheck{Drop: true, Cred: userspec.Credential{Uid: 1000, Name: "kahi"}})
	if u.Drop == nil || u.Drop.Uid != 1000 {
		t.Errorf("drop: %+v", u)
	}
}
