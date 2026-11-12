//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// Config file resolution (FUNC-005, SEC-017): the daemon takes -c, then
// KAHI_CONFIG, then the system paths only, never ./kahi.toml; kahi ctl also
// searches the current directory.

const searchedMsg = "no config file found; searched [/etc/kahi/kahi.toml /etc/kahi.toml]\n"

// requireNoSystemConfig skips a test whose expectations assume no config at
// the daemon's system search paths.
func requireNoSystemConfig(t *testing.T) {
	t.Helper()
	for _, p := range []string{"/etc/kahi/kahi.toml", "/etc/kahi.toml"} {
		if _, err := os.Stat(p); err == nil {
			t.Skipf("%s exists on this host", p)
		}
	}
}

// searchEnv is the test environment without KAHI_CONFIG, plus extra.
func searchEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "KAHI_CONFIG=") {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

// shortDir is a temp directory short enough for a Unix socket path.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "k-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// writeProgramConfig writes a config at path with a socket and one program.
func writeProgramConfig(t *testing.T, path, socket, program string) {
	t.Helper()
	cfg := fmt.Sprintf("[supervisor]\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n\n[programs.%s]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 2\n", socket, program)
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runKahiEnv runs kahi in dir with env and returns its output and exit code.
func runKahiEnv(t *testing.T, dir string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, kahiBinary, args...)
	cmd.Dir = dir
	cmd.Env = env
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
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("kahi %v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

// startSearchDaemon starts "kahi daemon args..." in dir with env and waits
// until it serves socket.
func startSearchDaemon(t *testing.T, dir, socket string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command(kahiBinary, append([]string{"daemon"}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	out := daemonOutput(t)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ctl.NewUnixClient(socket).Shutdown()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	waitForSocket(t, socket, 5*time.Second)
	waitForHealth(t, ctl.NewUnixClient(socket), 3*time.Second)
}

// wantPrograms runs "kahi ctl [ctlArgs] status" in dir with env and checks
// that it lists want and none of absent.
func wantPrograms(t *testing.T, dir string, env, ctlArgs []string, want string, absent ...string) {
	t.Helper()
	args := append(append([]string{"ctl"}, ctlArgs...), "status")
	out, errOut, code := runKahiEnv(t, dir, env, args...)
	if !strings.Contains(out, want) {
		t.Fatalf("kahi %v: exit %d, stdout %q, stderr %q; want %s listed", args, code, out, errOut, want)
	}
	for _, name := range absent {
		if strings.Contains(out, name) {
			t.Fatalf("kahi %v lists %s:\n%s", args, name, out)
		}
	}
}

// Without -c or KAHI_CONFIG the daemon ignores ./kahi.toml and searches only
// the system paths.
func TestConfigSearch_DaemonIgnoresCWD(t *testing.T) {
	t.Parallel()
	requireNoSystemConfig(t)
	dir := shortDir(t)
	socket := filepath.Join(dir, "kahi.sock")
	writeProgramConfig(t, filepath.Join(dir, "kahi.toml"), socket, "only_cwd")

	out, errOut, code := runKahiEnv(t, dir, searchEnv(), "daemon")
	if code != 1 || out != "" || errOut != searchedMsg {
		t.Fatalf("daemon: exit %d, stdout %q, stderr %q; want exit 1 and %q", code, out, errOut, searchedMsg)
	}
	if _, err := os.Stat(socket); err == nil {
		t.Fatal("the daemon created the socket named in ./kahi.toml")
	}
}

// -c ./kahi.toml and KAHI_CONFIG=./kahi.toml are explicit, so the daemon uses
// the file; kahi ctl finds ./kahi.toml on its own.
func TestConfigSearch_ExplicitCWDConfig(t *testing.T) {
	t.Parallel()
	for name, run := range map[string]struct {
		env  []string
		args []string
	}{
		"flag": {searchEnv(), []string{"-c", "./kahi.toml"}},
		"env":  {searchEnv("KAHI_CONFIG=./kahi.toml"), nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := shortDir(t)
			socket := filepath.Join(dir, "kahi.sock")
			writeProgramConfig(t, filepath.Join(dir, "kahi.toml"), socket, "only_cwd")
			startSearchDaemon(t, dir, socket, run.env, run.args...)
			wantPrograms(t, dir, searchEnv(), []string{"-s", socket}, "only_cwd")
			// No -s, -c or KAHI_CONFIG: kahi ctl takes its socket from ./kahi.toml.
			wantPrograms(t, dir, searchEnv(), nil, "only_cwd")
		})
	}
}

// -c beats KAHI_CONFIG, KAHI_CONFIG alone is used, and a -c symlink is
// followed to its target.
func TestConfigSearch_FlagAndEnv(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (dir, socket string) {
		dir = shortDir(t)
		socket = filepath.Join(dir, "kahi.sock")
		writeProgramConfig(t, filepath.Join(dir, "x.toml"), socket, "only_x")
		writeProgramConfig(t, filepath.Join(dir, "y.toml"), socket, "only_y")
		return dir, socket
	}
	t.Run("flag", func(t *testing.T) {
		t.Parallel()
		requireNoSystemConfig(t)
		dir, socket := setup(t)
		startSearchDaemon(t, dir, socket, searchEnv(), "-c", filepath.Join(dir, "x.toml"))
		wantPrograms(t, dir, searchEnv(), []string{"-s", socket}, "only_x", "only_y")
	})
	t.Run("env", func(t *testing.T) {
		t.Parallel()
		dir, socket := setup(t)
		startSearchDaemon(t, dir, socket, searchEnv("KAHI_CONFIG="+filepath.Join(dir, "y.toml")))
		wantPrograms(t, dir, searchEnv(), []string{"-s", socket}, "only_y", "only_x")
	})
	t.Run("flag beats env", func(t *testing.T) {
		t.Parallel()
		dir, socket := setup(t)
		startSearchDaemon(t, dir, socket, searchEnv("KAHI_CONFIG="+filepath.Join(dir, "y.toml")), "-c", filepath.Join(dir, "x.toml"))
		wantPrograms(t, dir, searchEnv(), []string{"-s", socket}, "only_x", "only_y")
	})
	t.Run("symlink", func(t *testing.T) {
		t.Parallel()
		dir, socket := setup(t)
		link := filepath.Join(dir, "link.toml")
		if err := os.Symlink(filepath.Join(dir, "x.toml"), link); err != nil {
			t.Fatal(err)
		}
		startSearchDaemon(t, dir, socket, searchEnv(), "-c", link)
		wantPrograms(t, dir, searchEnv(), []string{"-s", socket}, "only_x", "only_y")
	})
}

// An explicit config that cannot be read stops the daemon with the path and
// the reason; a missing KAHI_CONFIG does not fall back to the search paths.
func TestConfigSearch_UnreadableConfig(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	missing := filepath.Join(dir, "nope.toml")
	wantMissing := "cannot read config: " + missing + ": stat " + missing + ": no such file or directory\n"

	t.Run("missing flag", func(t *testing.T) {
		t.Parallel()
		out, errOut, code := runKahiEnv(t, dir, searchEnv(), "daemon", "-c", missing)
		if code != 1 || out != "" || errOut != wantMissing {
			t.Fatalf("exit %d, stdout %q, stderr %q; want exit 1 and %q", code, out, errOut, wantMissing)
		}
	})
	t.Run("missing env", func(t *testing.T) {
		t.Parallel()
		out, errOut, code := runKahiEnv(t, dir, searchEnv("KAHI_CONFIG="+missing), "daemon")
		if code != 1 || out != "" || errOut != wantMissing {
			t.Fatalf("exit %d, stdout %q, stderr %q; want exit 1 and %q", code, out, errOut, wantMissing)
		}
	})
	t.Run("permission denied", func(t *testing.T) {
		t.Parallel()
		if os.Geteuid() == 0 {
			t.Skip("root reads a mode 000 file")
		}
		path := filepath.Join(dir, "locked.toml")
		writeProgramConfig(t, path, filepath.Join(dir, "locked.sock"), "locked")
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		want := "cannot read config: " + path + ": open " + path + ": permission denied\n"
		out, errOut, code := runKahiEnv(t, dir, searchEnv(), "daemon", "-c", path)
		if code != 1 || out != "" || errOut != want {
			t.Fatalf("exit %d, stdout %q, stderr %q; want exit 1 and %q", code, out, errOut, want)
		}
	})
}
