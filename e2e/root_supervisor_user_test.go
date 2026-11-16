//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// execAs runs args in the container as user and returns the combined output.
func (c *rootContainer) execAs(user string, args ...string) (string, error) {
	out, err := exec.Command(c.rt, append([]string{"exec", "--user", user, c.name}, args...)...).CombinedOutput()
	return string(out), err
}

// stat returns orphaner's "uid gid mode" for path in the running container.
func (c *rootContainer) stat(path string) string {
	c.t.Helper()
	out, err := c.execAs("0", "/orphaner", "stat", path)
	if err != nil {
		c.t.Errorf("stat %s: %v: %s", path, err, out)
	}
	return strings.TrimSpace(out)
}

// cpOut copies path out of the (possibly stopped) container and returns its
// content, or an error when the copy fails.
func (c *rootContainer) cpOut(path string) (string, error) {
	dst := filepath.Join(c.t.TempDir(), "out")
	if out, err := exec.Command(c.rt, "cp", c.name+":"+path, dst).CombinedOutput(); err != nil {
		return "", &exec.ExitError{Stderr: out}
	}
	b, err := os.ReadFile(dst)
	return string(b), err
}

// exists reports whether a regular file exists in the (possibly stopped)
// container, from the exit code of cp. Not for sockets: podman versions
// differ (macOS: exit 0 without a file; Linux CI: an error).
func (c *rootContainer) exists(path string) bool {
	return exec.Command(c.rt, "cp", c.name+":"+path, filepath.Join(c.t.TempDir(), "out")).Run() == nil
}

// waitExit waits until the container has exited and returns its exit code
// and logs.
func (c *rootContainer) waitExit() (string, string) {
	c.t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		state, _ := exec.Command(c.rt, "inspect", "-f", "{{.State.Status}} {{.State.ExitCode}}", c.name).Output()
		if s := strings.TrimSpace(string(state)); strings.HasPrefix(s, "exited") {
			return strings.TrimPrefix(s, "exited "), c.logs()
		}
	}
	c.t.Fatalf("container still running after 20s; logs:\n%s", c.logs())
	return "", ""
}

// waitFileContains polls a file in the running container until it holds want.
func (c *rootContainer) waitFileContains(path, want string) string {
	c.t.Helper()
	var s string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if out, err := c.execAs("0", "/orphaner", "cat", path); err == nil {
			if s = out; strings.Contains(s, want) {
				return s
			}
		}
	}
	c.t.Fatalf("%s lacks %q after 20s:\n%s", path, want, s)
	return ""
}

const idProgram = `
[programs.id]
command = "/orphaner id"
startsecs = 0
autorestart = "false"
`

// [supervisor] user: the daemon drops to kahi (its groups from /etc/group,
// not the --group-add 999 it was started with), hands the socket, the socket
// directory it created, the PID file and its log to kahi, so kahi ctl works
// as kahi; a reload names the restart-only key; at shutdown the socket and
// the PID file are removed. No root warning (FUNC-045, FUNC-048, ADR-045).
func TestRootContainer_SupervisorUser(t *testing.T) {
	t.Parallel()
	cfg := func(user string) string {
		return `[supervisor]
user = "` + user + `"
logfile = "/run/kahi.log"
shutdown_timeout = 5

[server.unix]
file = "/run/kahi/kahi.sock"
` + idProgram
	}
	c := startRootContainer(t, usersImage(cfg("kahi")), []string{"--group-add", "999"}, []string{"-p", "/run/kahi/kahi.pid"})
	if got := c.waitOutput("id", "uid="); got != "uid=1000 euid=1000 gid=1000 egid=1000 groups=[4 1000]" {
		t.Errorf("id: %q, want kahi with groups [4 1000] (not 999)", got)
	}
	log := c.waitFileContains("/run/kahi.log", `"msg":"supervisor running"`)
	if !strings.Contains(log, `"msg":"dropped privileges"`) || strings.Contains(log, rootWarning) {
		t.Errorf("want a drop and no root warning; log:\n%s", log)
	}

	if out, err := c.execAs("1000", "/kahi", "ctl", "-s", "/run/kahi/kahi.sock", "status"); err != nil || !strings.Contains(out, "id") {
		t.Errorf("kahi ctl status as kahi: %v: %s", err, out)
	}
	for path, want := range map[string]string{
		"/run/kahi/kahi.sock": "1000 1000 0700",
		"/run/kahi":           "1000 1000 0700",
		"/run/kahi/kahi.pid":  "1000 1000 ",
		"/run/kahi.log":       "1000 1000 ",
	} {
		if got := c.stat(path); !strings.HasPrefix(got, want) {
			t.Errorf("stat %s = %q, want %q...", path, got, want)
		}
	}

	// A changed user takes effect only after a restart.
	newCfg := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(newCfg, []byte(cfg("app")), 0o644); err != nil {
		t.Fatal(err)
	}
	c.run("cp", newCfg, c.name+":/kahi.toml")
	if out, err := c.execAs("1000", "/kahi", "ctl", "-s", "/run/kahi/kahi.sock", "reload"); err != nil {
		t.Errorf("kahi ctl reload as kahi: %v: %s", err, out)
	}
	c.waitFileContains("/run/kahi.log", "supervisor.user change takes effect after restart")

	// Control for the check after the stop: cp finds the PID file while it
	// exists. The socket cannot be checked with cp (see exists); the
	// daemon's own shutdown check (RemoveRuntimeFiles, no warning below)
	// covers it, and TestRootContainer_SupervisorUserLeftovers shows that
	// check names a socket that stays.
	if !c.exists("/run/kahi/kahi.pid") {
		t.Fatal("control: cp does not find the PID file while it exists")
	}

	c.run("stop", "-t", "15", c.name)
	log, err := c.cpOut("/run/kahi.log")
	if err != nil {
		t.Fatalf("control: cannot copy the log from the stopped container: %v", err)
	}
	if !strings.Contains(log, `"msg":"supervisor stopped"`) && !strings.Contains(log, "shutdown") {
		t.Errorf("log shows no shutdown:\n%s", log)
	}
	if strings.Contains(log, "cannot remove at shutdown") {
		t.Errorf("files left at shutdown; log:\n%s", log)
	}
	if c.exists("/run/kahi/kahi.pid") {
		t.Error("the PID file still exists after shutdown")
	}
}

// Files in a directory the target user cannot write stay at shutdown, with
// one warning; the next start as root replaces them (FUNC-045).
func TestRootContainer_SupervisorUserLeftovers(t *testing.T) {
	t.Parallel()
	c := startRootContainer(t, usersImage(`[supervisor]
user = "kahi"
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"
`+idProgram), nil, []string{"-p", "/run/kahi.pid"})
	c.waitLogs(`"msg":"supervisor running"`)
	c.run("stop", "-t", "15", c.name)
	logs := c.logs()
	if n := linesWith(logs, "cannot remove at shutdown", "/run/kahi.pid", "/run/kahi.sock"); n != 1 {
		t.Errorf("%d warnings naming both files, want 1; logs:\n%s", n, logs)
	}
	if pid, err := c.cpOut("/run/kahi.pid"); err != nil || strings.TrimSpace(pid) == "" {
		t.Errorf("PID file: %q, %v; want it left in place", pid, err)
	}

	// The next start as root replaces both. The restarted daemon is PID 1
	// again, the PID the stale file names.
	c.run("start", c.name)
	for deadline := time.Now().Add(20 * time.Second); strings.Count(c.logs(), `"msg":"supervisor running"`) < 2; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no second start; logs:\n%s", c.logs())
		}
	}
}

// The forms of [supervisor] user, -u overriding it, and unknown users and
// groups refusing to start before anything is bound (FUNC-045).
func TestRootContainer_SupervisorUserForms(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, user string
		args       []string
		want       string
	}{
		{"name and group", "kahi:adm", nil, "uid=1000 euid=1000 gid=4 egid=4 groups=[4 1000]"},
		{"uid and group", "1000:adm", nil, "uid=1000 euid=1000 gid=4 egid=4 groups=[4 1000]"},
		{"uid with entry", "1000", nil, "uid=1000 euid=1000 gid=1000 egid=1000 groups=[4 1000]"},
		{"-u overrides", "kahi", []string{"-u", "app"}, "uid=1001 euid=1001 gid=1001 egid=1001 groups=[1001]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := startRootContainer(t, usersImage("[supervisor]\nuser = \""+tc.user+"\"\nshutdown_timeout = 5\n\n[server.unix]\nfile = \"/run/kahi.sock\"\n"+idProgram), nil, tc.args)
			if got := c.waitOutput("id", "uid="); got != tc.want {
				t.Errorf("id: %q, want %q", got, tc.want)
			}
			if tc.args != nil {
				if logs := c.logs(); !strings.Contains(logs, "kahi daemon -u app overrides supervisor.user") {
					t.Errorf("no override message; logs:\n%s", logs)
				}
			}
		})
	}
	for user, want := range map[string]string{"nosuch": "user not found: nosuch", "kahi:nosuch": "group not found: nosuch"} {
		t.Run(user, func(t *testing.T) {
			t.Parallel()
			c := startRootContainer(t, usersImage("[supervisor]\nuser = \""+user+"\"\n\n[server.unix]\nfile = \"/run/kahi.sock\"\n"+idProgram), nil, nil)
			code, logs := c.waitExit()
			if code != "1" || !strings.Contains(logs, want) {
				t.Errorf("exit %s, want 1 with %q; logs:\n%s", code, want, logs)
			}
			if strings.Contains(logs, `"msg":"unix socket server started"`) {
				t.Errorf("the socket was bound before the refusal; logs:\n%s", logs)
			}
		})
	}
}

// As non-root, [supervisor] user is ignored with a warning and the daemon
// starts; one config file is often shared between root and non-root runs.
func TestSupervisorUser_NonRootIgnored(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	dir := includeConfig(t, map[string]string{"kahi.toml": "[supervisor]\nuser = \"x\"\nshutdown_timeout = 5\n"})
	_, output := startIncludeDaemon(t, dir)
	out := waitFor(t, "daemon output", output, "supervisor running")
	if !strings.Contains(out, "supervisor.user ignored: dropping privileges needs root (running as uid") {
		t.Errorf("no ignored warning:\n%s", out)
	}
}

// kahi migrate maps [supervisord] user to [supervisor] user.
func TestMigrate_SupervisordUser(t *testing.T) {
	t.Parallel()
	out, _, code := migrate(t, "[supervisord]\nuser=kahi\n\n[program:web]\ncommand=/bin/sleep 300\n")
	sup, _, _ := strings.Cut(out, "[programs")
	if code != 0 || !strings.Contains(sup, "[supervisor]") || !strings.Contains(sup, `user = "kahi"`) {
		t.Errorf("exit %d; want user = \"kahi\" under [supervisor]:\n%s", code, out)
	}
}
