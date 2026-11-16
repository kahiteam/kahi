//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The user database the root container tests use (FUNC-045, FUNC-046,
// ADR-046): kahi 1000:1000 is also in adm (4).
const (
	testPasswd = "root:x:0:0:root:/root:/sbin/nologin\n" +
		"kahi:x:1000:1000::/home/kahi:/sbin/nologin\n" +
		"app:x:1001:1001::/home/app:/sbin/nologin\n" +
		"www-data:x:33:33:www-data:/var/www:/sbin/nologin\n"
	testGroup = "root:x:0:\nadm:x:4:kahi\nwww-data:x:33:\nkahi:x:1000:\napp:x:1001:\n"
)

// usersImage returns the files map for a container with the test user
// database and the given kahi.toml.
func usersImage(config string) map[string]string {
	return map[string]string{"etc/passwd": testPasswd, "etc/group": testGroup, "kahi.toml": config}
}

// status runs kahi ctl status for name inside the container as root.
func (c *rootContainer) status(socket, name string) string {
	c.t.Helper()
	out, _ := exec.Command(c.rt, "exec", c.name, "/kahi", "ctl", "-s", socket, "status", name).CombinedOutput()
	return string(out)
}

// waitStatus waits until name's status contains want.
func (c *rootContainer) waitStatus(socket, name, want string, timeout time.Duration) string {
	c.t.Helper()
	var s string
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if s = c.status(socket, name); strings.Contains(s, want) {
			return s
		}
	}
	c.t.Fatalf("status of %s lacks %q after %v: %q\nlogs:\n%s", name, want, timeout, s, c.logs())
	return ""
}

// linesWith counts the log lines that contain every one of parts.
func linesWith(logs string, parts ...string) int {
	n := 0
	for _, l := range strings.Split(logs, "\n") {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(l, p)
		}
		if all {
			n++
		}
	}
	return n
}

// Per-process users by name: the passwd uid and primary gid, the user's
// groups from /etc/group, a group override, a numeric uid without an
// entry, and unknown users and groups as spawn errors (FUNC-046, SEC-010).
func TestRootContainer_ProcessUserNames(t *testing.T) {
	t.Parallel()
	prog := func(name, user, extra string) string {
		return fmt.Sprintf("[programs.%s]\ncommand = \"/orphaner id\"\nuser = %q\nstartsecs = 0\nautorestart = \"false\"\n%s\n", name, user, extra)
	}
	c := startRootContainer(t, usersImage("[supervisor]\nshutdown_timeout = 5\n\n[server.unix]\nfile = \"/run/kahi.sock\"\n\n"+
		"[programs.rootid]\ncommand = \"/orphaner id\"\nstartsecs = 0\nautorestart = \"false\"\n\n"+
		prog("www", "www-data", "")+
		prog("kahiid", "kahi", "")+
		prog("kahiadm", "kahi:adm", "")+
		prog("numeric", "4242", "")+
		prog("nosuch", "nosuch", "startretries = 1")+
		prog("badgroup", "www-data:nosuch", "startretries = 0")), []string{"--group-add", "999"}, nil)
	c.waitLogs(`"msg":"supervisor running"`)
	// Control: the root daemon has 999, so its absence below is the switch.
	if got := c.waitOutput("rootid", "uid="); !strings.Contains(got, "999") {
		t.Errorf("rootid: %q, want group 999 from --group-add", got)
	}
	for name, want := range map[string]string{
		"www":     "uid=33 euid=33 gid=33 egid=33 groups=[33]",
		"kahiid":  "uid=1000 euid=1000 gid=1000 egid=1000 groups=[4 1000]",
		"kahiadm": "uid=1000 euid=1000 gid=4 egid=4 groups=[4 1000]",
		"numeric": "uid=4242 euid=4242 gid=4242 egid=4242 groups=[4242]",
	} {
		if got := c.waitOutput(name, "uid="); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}

	// Each start attempt is a spawn error: startretries = 1 means two
	// attempts, then FATAL; the program never runs.
	c.waitStatus("/run/kahi.sock", "nosuch", "FATAL", 15*time.Second)
	if s := c.status("/run/kahi.sock", "nosuch"); !strings.Contains(s, "spawn error: user not found: nosuch") {
		t.Errorf("nosuch status %q, want 'spawn error: user not found: nosuch'", s)
	}
	if n := linesWith(c.logs(), `"msg":"spawn failed"`, "user not found: nosuch"); n != 2 {
		t.Errorf("%d spawn failures for nosuch, want 2; logs:\n%s", n, c.logs())
	}
	if s := c.waitStatus("/run/kahi.sock", "badgroup", "FATAL", 10*time.Second); !strings.Contains(s, "spawn error: group not found: nosuch") {
		t.Errorf("badgroup status %q, want 'spawn error: group not found: nosuch'", s)
	}
	for _, name := range []string{"nosuch", "badgroup"} {
		if out := c.output(name); len(out) != 0 {
			t.Errorf("%s ran: %q", name, out)
		}
	}
}

// The user is looked up at each spawn: one added to /etc/passwd while the
// program backs off is picked up by the next retry (FUNC-046).
func TestRootContainer_ProcessUserCreatedLater(t *testing.T) {
	t.Parallel()
	c := startRootContainer(t, usersImage(`[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"

[programs.late]
command = "/orphaner id"
user = "late"
startsecs = 0
startretries = 10
autorestart = "false"
`), nil, nil)
	// Wait for a failed spawn, not the load warning that names the same
	// user: the replacement passwd must arrive after the first attempt.
	for deadline := time.Now().Add(20 * time.Second); linesWith(c.logs(), `"msg":"spawn failed"`, "user not found: late") == 0; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no failed spawn for late; logs:\n%s", c.logs())
		}
	}
	passwd := filepath.Join(t.TempDir(), "passwd")
	if err := os.WriteFile(passwd, []byte(testPasswd+"late:x:1002:1002::/home/late:/sbin/nologin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.run("cp", passwd, c.name+":/etc/passwd")
	if got := c.waitOutputWithin("late", "uid=", 30*time.Second); got != "uid=1002 euid=1002 gid=1002 egid=1002 groups=[1002]" {
		t.Errorf("late: %q, want uid 1002", got)
	}
}

// kahi daemon -u by name: the daemon drops to kahi's uid, primary gid and
// groups from /etc/group, discarding the groups it was started with
// (--group-add 999). A program user other than kahi gets the non-root
// warning and runs as kahi; one that is kahi runs without a warning
// (FUNC-045, FUNC-046, SEC-010).
func TestRootContainer_DropToName(t *testing.T) {
	t.Parallel()
	c := startRootContainer(t, usersImage(`[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"

[programs.inherit]
command = "/orphaner id"
startsecs = 0
autorestart = "false"

[programs.other]
command = "/orphaner id"
user = "www-data"
startsecs = 0
autorestart = "false"

[programs.self]
command = "/orphaner id"
user = "kahi"
startsecs = 0
autorestart = "false"
`), []string{"--group-add", "999"}, []string{"-u", "kahi"})
	logs := c.waitLogs(`"msg":"dropped privileges"`)
	if !strings.Contains(logs, `"uid":1000`) || !strings.Contains(logs, `"gid":1000`) {
		t.Errorf("drop not to 1000:1000; logs:\n%s", logs)
	}
	for _, name := range []string{"inherit", "other", "self"} {
		if got := c.waitOutput(name, "uid="); got != "uid=1000 euid=1000 gid=1000 egid=1000 groups=[4 1000]" {
			t.Errorf("%s: %q, want kahi with groups [4 1000] (not 999)", name, got)
		}
	}
	logs = c.logs()
	warn := "user switching unavailable (not running as root), running as current user"
	if linesWith(logs, warn, `"user":"www-data"`) == 0 {
		t.Errorf("no non-root warning for user www-data; logs:\n%s", logs)
	}
	if linesWith(logs, warn, `"user":"kahi"`) != 0 {
		t.Errorf("non-root warning for user kahi, the daemon's own identity; logs:\n%s", logs)
	}
	if strings.Contains(logs, rootWarning) {
		t.Errorf("root warning with -u; logs:\n%s", logs)
	}
}

// kahi daemon -u with an unknown user or group refuses before binding.
func TestRootContainer_DropUnknownUser(t *testing.T) {
	t.Parallel()
	for flag, want := range map[string]string{"nosuch": "user not found: nosuch", "kahi:nosuch": "group not found: nosuch"} {
		c := startRootContainer(t, usersImage("[server.unix]\nfile = \"/run/kahi.sock\"\n"), nil, []string{"-u", flag})
		var logs string
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
			state, _ := exec.Command(c.rt, "inspect", "-f", "{{.State.Status}} {{.State.ExitCode}}", c.name).Output()
			if strings.HasPrefix(string(state), "exited") {
				if !strings.HasPrefix(string(state), "exited 1") {
					t.Errorf("-u %s: container state %q, want exited 1", flag, state)
				}
				logs = c.logs()
				break
			}
		}
		if !strings.Contains(logs, want) {
			t.Errorf("-u %s: logs lack %q:\n%s", flag, want, logs)
		}
		if strings.Contains(logs, `"msg":"unix socket server started"`) {
			t.Errorf("-u %s: the socket was bound before the refusal; logs:\n%s", flag, logs)
		}
	}
}

// As non-root, an unknown program user is reported as a config warning at
// load and on reload, before anything spawns (FUNC-046).
func TestProcessUser_UnknownUserWarning(t *testing.T) {
	t.Parallel()
	dir := includeConfig(t, map[string]string{"kahi.toml": "[programs.p]\ncommand = \"/bin/sleep 300\"\nuser = \"nosuchuser\"\nstartsecs = 0\nstopwaitsecs = 2\n"})
	client, output := startIncludeDaemon(t, dir)
	waitForState(t, client, "p", "RUNNING", 5*time.Second)
	out := output()
	warn, running := strings.Index(out, "programs.p.user: user not found: nosuchuser"), strings.Index(out, "supervisor running")
	if warn < 0 || running < 0 || warn > running {
		t.Errorf("warning at %d, running at %d; want the warning first:\n%s", warn, running, out)
	}

	socket := filepath.Join(dir, "kahi.sock")
	cfg := "[programs.p]\ncommand = \"/bin/sleep 300\"\nuser = \"nosuchuser2\"\nstartsecs = 0\nstopwaitsecs = 2\n\n[server.unix]\nfile = \"" + socket + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "kahi.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runKahi(t, dir, "ctl", "-s", socket, "reload")
	if code != 0 || !strings.Contains(stderr, "programs.p.user: user not found: nosuchuser2") {
		t.Errorf("ctl reload: exit %d, stdout %q, stderr %q; want the user warning", code, stdout, stderr)
	}
}
