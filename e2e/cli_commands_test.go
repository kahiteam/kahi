//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/kahiteam/kahi/internal/config"
)

// kahi init (FUNC-076), kahi ctl status (FUNC-034) and kahi hash-password
// (SEC-018), run as the binary.

func TestCLI_InitStdoutAndFiles(t *testing.T) {
	dir := t.TempDir()

	// --stdout prints the template and writes nothing.
	out, stderr, code := runKahi(t, dir, "init", "--stdout")
	if code != 0 || stderr != "" || out != config.DefaultConfigTOML {
		t.Fatalf("init --stdout: exit %d, stderr %q, stdout differs from the template: %v", code, stderr, out != config.DefaultConfigTOML)
	}
	if _, err := os.Stat(filepath.Join(dir, "kahi.toml")); !os.IsNotExist(err) {
		t.Fatal("init --stdout wrote kahi.toml")
	}
	if _, _, err := config.LoadBytes([]byte(out), "init.toml"); err != nil {
		t.Fatalf("the template does not load: %v", err)
	}

	// Without flags it writes ./kahi.toml; -o names another file.
	for _, args := range [][]string{{"init"}, {"init", "-o", "other.toml"}, {"init", "--output", "third.toml"}} {
		name := "kahi.toml"
		if len(args) == 3 {
			name = args[2]
		}
		out, stderr, code := runKahi(t, dir, args...)
		if code != 0 || out != "wrote "+name+"\n" || stderr != "" {
			t.Fatalf("%v: exit %d, stdout %q, stderr %q", args, code, out, stderr)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != config.DefaultConfigTOML {
			t.Fatalf("%v: %s is not the template", args, name)
		}
	}

	// An existing file is kept unless --force is given.
	path := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(path, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = runKahi(t, dir, "init")
	if code != 1 || stderr != "file kahi.toml already exists; use --force to overwrite\n" {
		t.Fatalf("init over an existing file: exit %d, stderr %q", code, stderr)
	}
	if b, _ := os.ReadFile(path); string(b) != "# mine\n" {
		t.Fatalf("refused init changed the file: %q", b)
	}
	if out, _, code := runKahi(t, dir, "init", "--force"); code != 0 || out != "wrote kahi.toml\n" {
		t.Fatalf("init --force: exit %d, stdout %q", code, out)
	}
	if b, _ := os.ReadFile(path); string(b) != config.DefaultConfigTOML {
		t.Fatal("init --force did not overwrite")
	}
}

// The template documents every config key a user can set, each as a
// commented-out line. Left out: server.unix.chown, which is refused
// (SEC-022), and the keys accepted but ignored with a warning until #113
// and #152 implement them.
var notInTemplate = map[string]bool{
	"chown": true, "stdout_capture_maxbytes": true, "stderr_capture_maxbytes": true,
	"stdout_syslog": true, "stderr_syslog": true,
}

func TestCLI_InitDocumentsEveryKey(t *testing.T) {
	out, _, code := runKahi(t, t.TempDir(), "init", "--stdout")
	if code != 0 {
		t.Fatalf("init --stdout: exit %d", code)
	}
	for section, v := range map[string]any{
		"supervisor":  config.SupervisorConfig{},
		"server.unix": config.UnixServerConfig{},
		"server.http": config.HTTPServerConfig{},
		"server.web":  config.WebServerConfig{},
		"programs":    config.ProgramConfig{},
		"groups":      config.GroupConfig{},
		"webhooks":    config.WebhookConfig{},
	} {
		typ := reflect.TypeOf(v)
		for i := 0; i < typ.NumField(); i++ {
			key := strings.Split(typ.Field(i).Tag.Get("toml"), ",")[0]
			if key == "" || key == "-" || notInTemplate[key] {
				continue
			}
			if !regexp.MustCompile(`(?m)^#\s*`+regexp.QuoteMeta(key)+`\s*=`).MatchString(out) &&
				!regexp.MustCompile(`(?m)^#\s*\[[^\]]*\.`+regexp.QuoteMeta(key)+`\]`).MatchString(out) {
				t.Errorf("[%s] %s has no commented-out line in the template", section, key)
			}
		}
	}
}

// The generated file starts a daemon with no programs. Only the socket path
// is changed, so the test does not need /var/run (#146).
func TestCLI_InitConfigStartsDaemon(t *testing.T) {
	dir := shortTempDir(t)
	if _, _, code := runKahi(t, dir, "init"); code != 0 {
		t.Fatal("init failed")
	}
	path := filepath.Join(dir, "kahi.toml")
	b, _ := os.ReadFile(path)
	socket := filepath.Join(dir, "kahi.sock")
	cfg := strings.Replace(string(b), `# file = "/var/run/kahi.sock"`, `file = "`+socket+`"`, 1)
	if cfg == string(b) {
		t.Fatal("template has no commented socket line to set")
	}
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := startDaemonCmd(t, kahiBinary, path, dir)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitForSocket(t, socket, 5*time.Second)
	out, _, code := runKahi(t, dir, "ctl", "-s", socket, "status", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("status of the init config: exit %d, %q; want no programs", code, out)
	}
}

func TestCLI_StatusTableContents(t *testing.T) {
	dir := t.TempDir()
	client, socket := startDaemon(t, `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0
description = "front end"

[programs.done]
command = "`+writeScript(t, dir, "done.sh", "exit 3")+`"
startsecs = 0
autorestart = "false"
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	waitForState(t, client, "done", "EXITED", 5*time.Second)
	time.Sleep(1200 * time.Millisecond) // uptime shows "-" until it is 1s
	info, _ := getProcessInfo(client, "web")

	out, _, code := runKahi(t, dir, "ctl", "-s", socket, "status")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if code != 0 || len(lines) != 3 {
		t.Fatalf("status: exit %d, %q", code, out)
	}
	if got := strings.Join(strings.Fields(lines[0]), " "); got != "NAME STATE PID UPTIME DESCRIPTION" {
		t.Errorf("header %q", got)
	}
	// Rows sorted by name; EXITED shows its exit code, RUNNING its pid,
	// uptime and description.
	if f := strings.Fields(lines[1]); len(f) != 6 || f[0] != "done" || f[1] != "EXITED" || f[2] != "-" || f[3] != "-" || f[4] != "exit" || f[5] != "3" {
		t.Errorf("done row %q", lines[1])
	}
	if f := strings.Fields(lines[2]); len(f) != 6 || f[0] != "web" || f[1] != "RUNNING" || f[2] != strconv.Itoa(info.PID) ||
		!regexp.MustCompile(`^\d+m$`).MatchString(f[3]) || f[4]+" "+f[5] != "front end" {
		t.Errorf("web row %q", lines[2])
	}

	// A name narrows the table to that process.
	out, _, _ = runKahi(t, dir, "ctl", "-s", socket, "status", "web")
	if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[1], "web ") {
		t.Errorf("status web: %q", out)
	}
	// No match: the header only, and [] as JSON (#189).
	out, _, code = runKahi(t, dir, "ctl", "-s", socket, "status", "nosuch")
	if code != 0 || len(strings.Split(strings.TrimRight(out, "\n"), "\n")) != 1 {
		t.Errorf("status nosuch: exit %d, %q", code, out)
	}
	out, _, _ = runKahi(t, dir, "ctl", "-s", socket, "status", "--json", "nosuch")
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("status --json nosuch: %q, want []", out)
	}
	var procs []map[string]any
	out, _, _ = runKahi(t, dir, "ctl", "-s", socket, "status", "--json", "done")
	if json.Unmarshal([]byte(out), &procs) != nil || len(procs) != 1 || procs[0]["exitstatus"] != float64(3) {
		t.Errorf("status --json done: %q", out)
	}
}

// With no programs the table is the header line alone.
func TestCLI_StatusEmpty(t *testing.T) {
	_, socket := startDaemon(t, "")
	out, _, code := runKahi(t, t.TempDir(), "ctl", "-s", socket, "status")
	if code != 0 || strings.Join(strings.Fields(out), " ") != "NAME STATE PID UPTIME DESCRIPTION" {
		t.Fatalf("status with no programs: exit %d, %q", code, out)
	}
}

var bcryptHash = regexp.MustCompile(`^\$2a\$10\$[./A-Za-z0-9]{53}$`)

// Piped: the first line is hashed and only the hash is printed.
func TestCLI_HashPasswordPiped(t *testing.T) {
	cmd := exec.Command(kahiBinary, "hash-password")
	cmd.Stdin = strings.NewReader("mypassword\nignored\n")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("hash-password: %v, stderr %q", err, stderr.String())
	}
	hash := strings.TrimSuffix(string(out), "\n")
	if !bcryptHash.MatchString(hash) || string(out) != hash+"\n" {
		t.Fatalf("output %q, want one bcrypt hash line", out)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("mypassword")) != nil {
		t.Fatal("hash does not match the password")
	}

	for input, want := range map[string]string{
		"\n":    "password cannot be empty\n",
		"   \n": "password cannot be empty\n",
		"":      "no input received\n",
	} {
		cmd := exec.Command(kahiBinary, "hash-password")
		cmd.Stdin = strings.NewReader(input)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || stderr.String() != want || len(out) != 0 {
			t.Errorf("input %q: err %v, stdout %q, stderr %q; want exit 1 and %q", input, err, out, stderr.String(), want)
		}
	}
}

// On a terminal it prompts on stderr and reads without echo.
func TestCLI_HashPasswordTerminal(t *testing.T) {
	full := []string{kahiBinary, "hash-password"}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("script", append([]string{"-q", "/dev/null"}, full...)...)
	} else {
		cmd = exec.Command("script", "-qec", strings.Join(full, " "), "/dev/null")
	}
	// The input arrives after the prompt, once echo is off; typed earlier,
	// the terminal would echo it before kahi could turn echo off.
	pr, pw := io.Pipe()
	cmd.Stdin = pr
	go func() {
		time.Sleep(time.Second)
		_, _ = io.WriteString(pw, "s3cretpw\n")
		time.Sleep(time.Second)
		_ = pw.Close()
	}()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script: %v (%q)", err, out)
	}
	text := strings.ReplaceAll(string(out), "\r", "")
	if !strings.HasPrefix(text, "Password: ") {
		t.Errorf("no prompt: %q", text)
	}
	if strings.Contains(text, "s3cretpw") {
		t.Errorf("the password was echoed: %q", text)
	}
	var hash string
	for _, line := range strings.Split(text, "\n") {
		if m := regexp.MustCompile(`\$2a\$10\$[./A-Za-z0-9]{53}`).FindString(line); m != "" {
			hash = m
		}
	}
	if hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("s3cretpw")) != nil {
		t.Fatalf("no matching hash in %q", text)
	}
}
