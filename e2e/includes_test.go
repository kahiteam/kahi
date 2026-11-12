//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// Config file inclusion (FUNC-007). Every daemon here runs in a working
// directory other than the config's, so relative includes are shown to
// resolve against the config directory, not the cwd.

// includeConfig writes files (relative path → content) into a short temp
// dir; "kahi.toml" gets the [server.unix] table appended, after its own
// top-level keys. It returns the dir.
func includeConfig(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := shortDir(t)
	for name, content := range files {
		if name == "kahi.toml" {
			content += "\n[server.unix]\nfile = \"" + filepath.Join(dir, "kahi.sock") + "\"\n"
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// startIncludeDaemon starts the daemon on dir/kahi.toml from another
// directory and returns a client and the daemon's output so far.
func startIncludeDaemon(t *testing.T, dir string) (*ctl.Client, func() string) {
	t.Helper()
	cmd := exec.Command(kahiBinary, "daemon", "-c", filepath.Join(dir, "kahi.toml"))
	cmd.Dir = t.TempDir()
	out := daemonOutput(t)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "kahi.sock")
	t.Cleanup(func() { stopDaemon(t, cmd, socket) })
	waitForSocket(t, socket, 5*time.Second)
	client := ctl.NewUnixClient(socket)
	waitForHealth(t, client, 3*time.Second)
	return client, func() string { b, _ := os.ReadFile(out.Name()); return string(b) }
}

// refusedInclude runs the daemon on dir/kahi.toml from another directory,
// expecting it to refuse the config, and returns its stderr.
func refusedInclude(t *testing.T, dir string) string {
	t.Helper()
	_, stderr, code := runKahi(t, t.TempDir(), "daemon", "-c", filepath.Join(dir, "kahi.toml"))
	if code != 1 {
		t.Fatalf("daemon: exit %d, want 1; stderr %q", code, stderr)
	}
	return stderr
}

const sleeper = "command = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\n"

// Matching files are merged in name order (a later [groups] table wins),
// a relative pattern resolves against the config dir, and a pattern that
// matches nothing is a warning, not an error.
func TestInclude_MergeOrderAndMissingPattern(t *testing.T) {
	t.Parallel()
	dir := includeConfig(t, map[string]string{
		"kahi.toml":        "include = [\"conf.d/*.toml\", \"none/*.toml\"]\n\n[programs.x]\n" + sleeper + "\n[programs.y]\n" + sleeper,
		"conf.d/02-a.toml": "[programs.alpha]\n" + sleeper + "\n[groups.g]\nprograms = [\"x\"]\n",
		"conf.d/10-b.toml": "[programs.beta]\n" + sleeper + "\n[groups.g]\nprograms = [\"y\"]\n",
	})
	client, output := startIncludeDaemon(t, dir)
	for _, name := range []string{"alpha", "beta", "x", "y"} {
		waitForState(t, client, name, "RUNNING", 5*time.Second)
	}
	// 10-b.toml is merged after 02-a.toml, so its [groups.g] wins: y is in
	// g, and x falls back to a group of its own.
	if x, _ := getProcessInfo(client, "x"); x.Group != "x" {
		t.Errorf("x in group %q, want x (02-a.toml's [groups.g] replaced)", x.Group)
	}
	if y, _ := getProcessInfo(client, "y"); y.Group != "g" {
		t.Errorf("y in group %q, want g (from 10-b.toml, merged last)", y.Group)
	}
	want := "warning: include pattern \"" + filepath.Join(dir, "none", "*.toml") + "\" matched no files"
	if !strings.Contains(output(), want) {
		t.Errorf("daemon output lacks %q:\n%s", want, output())
	}
}

// A syntax error, a cycle and a program defined in two included files each
// stop the daemon, naming the file(s).
func TestInclude_Errors(t *testing.T) {
	t.Parallel()
	t.Run("syntax error", func(t *testing.T) {
		t.Parallel()
		dir := includeConfig(t, map[string]string{
			"kahi.toml":       "include = [\"conf.d/*.toml\"]\n",
			"conf.d/bad.toml": "[[invalid\n",
		})
		bad := filepath.Join(dir, "conf.d", "bad.toml")
		if stderr := refusedInclude(t, dir); !strings.Contains(stderr, "include "+bad+": config parse error in "+bad) {
			t.Errorf("stderr %q does not name %s", stderr, bad)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		t.Parallel()
		dir := includeConfig(t, map[string]string{
			"kahi.toml":        "include = [\"conf.d/*.toml\"]\n",
			"conf.d/loop.toml": "include = [\"../kahi.toml\"]\n",
		})
		if stderr := refusedInclude(t, dir); !strings.Contains(stderr, "circular include detected: "+filepath.Join(dir, "kahi.toml")) {
			t.Errorf("stderr %q, want the cycle back to kahi.toml", stderr)
		}
	})
	t.Run("duplicate program", func(t *testing.T) {
		t.Parallel()
		dir := includeConfig(t, map[string]string{
			"kahi.toml":      "include = [\"conf.d/*.toml\"]\n",
			"conf.d/01.toml": "[programs.web]\n" + sleeper,
			"conf.d/02.toml": "[programs.web]\n" + sleeper,
		})
		want := "duplicate program name \"web\": defined in both " + filepath.Join(dir, "conf.d", "01.toml") + " and " + filepath.Join(dir, "conf.d", "02.toml")
		if stderr := refusedInclude(t, dir); !strings.Contains(stderr, want) {
			t.Errorf("stderr %q lacks %q", stderr, want)
		}
	})
}

// An included file defines programs, but its [supervisor] table is ignored
// with a warning: the main file's settings stay in effect.
func TestInclude_SupervisorNotOverridden(t *testing.T) {
	t.Parallel()
	dir := includeConfig(t, map[string]string{
		"kahi.toml":  "include = [\"extra.toml\"]\n\n[supervisor]\nshutdown_timeout = 10\n",
		"extra.toml": "[supervisor]\nshutdown_timeout = 99\n\n[programs.web]\n" + sleeper,
	})
	client, output := startIncludeDaemon(t, dir)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	want := "warning: " + filepath.Join(dir, "extra.toml") + ": [supervisor] in an included file is ignored; set it in the main config"
	if !strings.Contains(output(), want) {
		t.Errorf("daemon output lacks %q:\n%s", want, output())
	}
	var cfg struct{ Supervisor struct{ ShutdownTimeout int } }
	apiDo(t, filepath.Join(dir, "kahi.sock"), "GET", "/api/v1/config", "").jsonBody(t, "config", http.StatusOK, &cfg)
	if cfg.Supervisor.ShutdownTimeout != 10 {
		t.Errorf("shutdown_timeout %d, want the main file's 10", cfg.Supervisor.ShutdownTimeout)
	}
}
