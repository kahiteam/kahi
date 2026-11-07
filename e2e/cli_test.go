//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
)

// The other e2e tests drive the daemon through the Go client library. These
// run the kahi binary itself, so printed text, colours and exit codes are
// covered end to end (#130).

// runKahi runs the kahi binary in dir and returns its stdout, stderr and exit
// code.
func runKahi(t *testing.T, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, kahiBinary, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("kahi %v did not finish within 15s", args)
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("kahi %v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

// runKahiTTY runs the kahi binary with stdout on a pseudo-terminal, using the
// script utility, and returns everything it printed.
func runKahiTTY(t *testing.T, args ...string) string {
	t.Helper()
	full := append([]string{kahiBinary}, args...)
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("script", append([]string{"-q", "/dev/null"}, full...)...)
	} else {
		quoted := make([]string, len(full))
		for i, a := range full {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		cmd = exec.Command("script", "-qec", strings.Join(quoted, " "), "/dev/null")
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script %v: %v (output %q)", args, err, out)
	}
	// script adds terminal artefacts: CR line endings, and on macOS the
	// echoed EOF from its stdin, "^D" followed by two backspaces.
	return ttyNoise.ReplaceAllString(strings.ReplaceAll(string(out), "^D\b\b", ""), "")
}

// ttyNoise matches control characters other than newline, tab and ESC.
var ttyNoise = regexp.MustCompile("[\x00-\x08\x0b-\x1a\x1c-\x1f]")

const stableProgram = `
[programs.web]
command = "/bin/sleep 300"
autostart = true
startsecs = 0

[programs.api]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`

func TestCLI_HealthOK(t *testing.T) {
	client, socketPath := startDaemon(t, stableProgram)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)

	out, _, code := runKahi(t, t.TempDir(), "ctl", "-s", socketPath, "health")
	if code != 0 || strings.TrimSpace(out) != "OK" {
		t.Fatalf("health: exit %d, output %q; want exit 0 and OK", code, out)
	}
}

// A socket that accepts and drops connections stands in for a daemon that
// does not respond.
func TestCLI_HealthNotResponsive(t *testing.T) {
	dir, err := os.MkdirTemp("", "k-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "k.sock")
	ln, err := net.Listen("unix", socketPath)
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

	_, stderr, code := runKahi(t, dir, "ctl", "-s", socketPath, "health")
	if code != 1 || strings.TrimSpace(stderr) == "" {
		t.Fatalf("health against an unresponsive socket: exit %d, stderr %q; want exit 1 with an error", code, stderr)
	}
}

func TestCLI_HealthCannotConnect(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runKahi(t, dir, "ctl", "-s", filepath.Join(dir, "missing.sock"), "health")
	if code != 1 || !strings.Contains(stderr, "connection failed") {
		t.Fatalf("health without a daemon: exit %d, stderr %q; want exit 1 with a connection error", code, stderr)
	}
}

func TestCLI_Ready(t *testing.T) {
	client, socketPath := startDaemon(t, stableProgram)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	waitForState(t, client, "api", "RUNNING", 5*time.Second)

	for _, args := range [][]string{
		{"ctl", "-s", socketPath, "ready"},
		{"ctl", "-s", socketPath, "ready", "--process", "web,api"},
	} {
		out, _, code := runKahi(t, t.TempDir(), args...)
		if code != 0 || strings.TrimSpace(out) != "READY" {
			t.Fatalf("%v: exit %d, output %q; want exit 0 and READY", args, code, out)
		}
	}
}

func TestCLI_ReadyPending(t *testing.T) {
	client, socketPath := startDaemon(t, `
[programs.slow]
command = "/bin/sleep 300"
autostart = true
startsecs = 60
`)
	waitForState(t, client, "slow", "STARTING", 5*time.Second)

	out, _, code := runKahi(t, t.TempDir(), "ctl", "-s", socketPath, "ready")
	if code != 1 || strings.TrimSpace(out) != "NOT READY" {
		t.Fatalf("ready while starting: exit %d, output %q; want exit 1 and NOT READY", code, out)
	}
}

// statusDaemon runs one RUNNING, one STARTING and one FATAL program.
func statusDaemon(t *testing.T) string {
	t.Helper()
	client, socketPath := startDaemon(t, `
[programs.good]
command = "/bin/sleep 300"
autostart = true
startsecs = 0

[programs.slow]
command = "/bin/sleep 300"
autostart = true
startsecs = 60

[programs.broken]
command = "/bin/false"
autostart = true
startsecs = 1
startretries = 0
`)
	waitForState(t, client, "good", "RUNNING", 5*time.Second)
	waitForState(t, client, "slow", "STARTING", 5*time.Second)
	waitForState(t, client, "broken", "FATAL", 10*time.Second)
	return socketPath
}

var ansiCode = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Columns line up in every row, with and without colour.
func checkAligned(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(ansiCode.ReplaceAllString(out, ""), "\n"), "\n")
	pidCol := strings.Index(lines[0], "PID")
	if pidCol < 0 {
		t.Fatalf("no PID header in %q", out)
	}
	for _, line := range lines[1:] {
		if len(line) <= pidCol || line[pidCol-1] != ' ' || line[pidCol] == ' ' {
			t.Fatalf("PID column not aligned at %d in %q\n%s", pidCol, line, strings.Join(lines, "\n"))
		}
	}
}

func TestCLI_StatusPipedHasNoColour(t *testing.T) {
	socketPath := statusDaemon(t)

	out, _, code := runKahi(t, t.TempDir(), "ctl", "-s", socketPath, "status")
	if code != 0 {
		t.Fatalf("status exit %d", code)
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("piped status contains colour codes: %q", out)
	}
	checkAligned(t, out)
}

func TestCLI_StatusTerminalColours(t *testing.T) {
	socketPath := statusDaemon(t)

	out := runKahiTTY(t, "ctl", "-s", socketPath, "status")
	for state, code := range map[string]string{"RUNNING": "32", "FATAL": "31", "STARTING": "33"} {
		if !strings.Contains(out, "\x1b["+code+"m"+state) {
			t.Errorf("terminal status lacks \\033[%sm for %s: %q", code, state, out)
		}
	}
	checkAligned(t, out)

	plain := runKahiTTY(t, "ctl", "-s", socketPath, "status", "--no-color")
	if strings.Contains(plain, "\x1b[3") {
		t.Fatalf("--no-color status on a terminal contains colour codes: %q", plain)
	}
}

func TestCLI_StatusJSON(t *testing.T) {
	socketPath := statusDaemon(t)

	out, _, code := runKahi(t, t.TempDir(), "ctl", "-s", socketPath, "status", "--json")
	var procs []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &procs) != nil || len(procs) != 3 {
		t.Fatalf("status --json: exit %d, output %q; want a JSON array of 3 processes", code, out)
	}
}

func TestCLI_SendWritesLineToStdin(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "cat.sh", "while IFS= read -r line; do printf 'got:%s\\n' \"$line\"; done")
	client, socketPath := startDaemon(t, `
[programs.cat]
command = "`+script+`"
autostart = true
startsecs = 0

[programs.idle]
command = "/bin/sleep 300"
autostart = false
`)
	waitForState(t, client, "cat", "RUNNING", 5*time.Second)

	if _, stderr, code := runKahi(t, dir, "ctl", "-s", socketPath, "send", "cat", "quit"); code != 0 {
		t.Fatalf("send: exit %d, stderr %q", code, stderr)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var buf bytes.Buffer
		// "got:quit" proves the line arrived with its trailing newline:
		// read only returns a complete line.
		if err := client.Tail("cat", "stdout", 4096, &buf); err == nil && strings.Contains(buf.String(), "got:quit") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process never received the line sent with ctl send")
		}
		time.Sleep(50 * time.Millisecond)
	}

	_, stderr, code := runKahi(t, dir, "ctl", "-s", socketPath, "send", "idle", "x")
	if code == 0 || !strings.Contains(stderr, "not running") {
		t.Fatalf("send to a stopped process: exit %d, stderr %q; want a not running error", code, stderr)
	}
}

const supervisordConf = `[program:web]
command=/bin/sleep 300
stopsignal=SIGINT
environment=A="1",B="two"
stdout_events_enabled=true
stderr_events_enabled=true
`

func TestCLI_MigrateToStdout(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "supervisord.conf"), []byte(supervisordConf), 0644); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"migrate", "supervisord.conf"},
		{"migrate", "supervisord.conf", "--dry-run"},
		{"migrate", "supervisord.conf", "--dry-run", "--output", "kahi.toml"},
	} {
		out, stderr, code := runKahi(t, dir, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", args, code, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "kahi.toml")); err == nil {
			t.Fatalf("%v wrote kahi.toml; a dry run must not write files", args)
		}
		cfg, _, err := config.LoadBytes([]byte(out), "migrated")
		if err != nil {
			t.Fatalf("%v printed TOML kahi cannot load: %v\n%s", args, err, out)
		}
		web := cfg.Programs["web"]
		if web.Command != "/bin/sleep 300" || web.Stopsignal != "SIGINT" ||
			web.Environment["A"] != "1" || web.Environment["B"] != "two" {
			t.Fatalf("%v: web = %+v, want command, SIGINT and environment mapped", args, web)
		}
		for _, opt := range []string{"stdout_events_enabled", "stderr_events_enabled"} {
			if !strings.Contains(stderr, `unsupported option "`+opt+`"`) {
				t.Fatalf("%v: no warning for unsupported %s on stderr: %q", args, opt, stderr)
			}
			if !strings.Contains(out, "# UNSUPPORTED: "+opt) {
				t.Fatalf("%v: no TOML comment for unsupported %s:\n%s", args, opt, out)
			}
		}
	}
}

func TestCLI_MigrateOutputFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "supervisord.conf"), []byte(supervisordConf), 0644); err != nil {
		t.Fatal(err)
	}

	if _, stderr, code := runKahi(t, dir, "migrate", "supervisord.conf", "--output", "kahi.toml"); code != 0 {
		t.Fatalf("migrate --output: exit %d, stderr %q", code, stderr)
	}
	if _, _, err := config.LoadWithIncludes(filepath.Join(dir, "kahi.toml")); err != nil {
		t.Fatalf("written kahi.toml does not load: %v", err)
	}

	_, stderr, code := runKahi(t, dir, "migrate", "supervisord.conf", "--output", "kahi.toml")
	if code != 1 || !strings.Contains(stderr, "output file exists") {
		t.Fatalf("overwrite without --force: exit %d, stderr %q; want a refusal", code, stderr)
	}
}

func TestCLI_MigrateErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.conf"), []byte("this is not ini\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runKahi(t, dir, "migrate", "nonexistent.conf")
	if code != 1 || !strings.Contains(stderr, "file not found") {
		t.Fatalf("missing input: exit %d, stderr %q; want file not found", code, stderr)
	}
	_, stderr, code = runKahi(t, dir, "migrate", "bad.conf")
	if code != 1 || !strings.Contains(stderr, "parse error at line 1") {
		t.Fatalf("invalid input: exit %d, stderr %q; want a parse error with the line", code, stderr)
	}
}
