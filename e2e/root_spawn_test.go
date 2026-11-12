//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Resource limits, umask and the environment of a child whose user differs
// from the daemon's, seen by the child itself, as root in a container
// (ADR-037). Limits are only lowered: a rootless runtime cannot raise them.

// The child runs with its configured NOFILE and NPROC limits and umask; a
// child with neither inherits the daemon's (FUNC-057, FUNC-058).
func TestRootContainer_RLimitsAndUmask(t *testing.T) {
	t.Parallel()
	c := startRootContainer(t, map[string]string{"kahi.toml": `[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"

[programs.nofile]
command = "/orphaner limits"
startsecs = 0
autorestart = "false"
[programs.nofile.environment]
KAHI_RLIMIT_NOFILE = "256"

# NPROC counts every process of the uid, so this runs as a uid of its own.
[programs.nproc]
command = "/orphaner limits"
user = "65534:65534"
startsecs = 0
autorestart = "false"
[programs.nproc.environment]
KAHI_RLIMIT_NPROC = "512"

[programs.masked]
command = "/orphaner umask /run/masked"
umask = "0027"
startsecs = 0
autorestart = "false"

[programs.plainlimits]
command = "/orphaner limits"
startsecs = 0
autorestart = "false"

[programs.plainumask]
command = "/orphaner umask /run/plain"
startsecs = 0
autorestart = "false"
`}, nil, nil)
	c.waitLogs(`"msg":"supervisor running"`)

	if got := c.waitOutput("nofile", "NOFILE "); got != "NOFILE 256 256" {
		t.Errorf("nofile: %q, want NOFILE 256 256", got)
	}
	if got := c.waitOutput("nproc", "NPROC "); got != "NPROC 512 512" {
		t.Errorf("nproc: %q, want NPROC 512 512", got)
	}
	if got := c.waitOutput("masked", "umask "); got != "umask 0027" {
		t.Errorf("masked: %q, want umask 0027", got)
	}
	if got := c.waitOutput("masked", "mode "); got != "mode 0640" {
		t.Errorf("masked: %q, want a 0666 create to give mode 0640", got)
	}

	// Without either setting the child has the daemon's limits and umask:
	// those the runtime gives PID 1, and gives a process run with exec.
	ref := strings.Split(strings.TrimSpace(c.run("exec", c.name, "/orphaner", "limits")), "\n")
	for _, want := range ref {
		if got := c.waitOutput("plainlimits", strings.Fields(want)[0]+" "); got != want {
			t.Errorf("plainlimits: %q, want the daemon's %q", got, want)
		}
	}
	refUmask := strings.TrimSpace(c.run("exec", c.name, "/orphaner", "umask"))
	if got := c.waitOutput("plainumask", "umask "); got != refUmask {
		t.Errorf("plainumask: %q, want the daemon's %q", got, refUmask)
	}
}

// An rlimit value that is not a number is a config error, not dropped at
// spawn (FUNC-057).
func TestRLimit_InvalidValueRefused(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	cfg := filepath.Join(dir, "kahi.toml")
	body := "[server.unix]\nfile = \"" + filepath.Join(dir, "kahi.sock") + "\"\n\n[programs.web]\ncommand = \"/bin/sleep 300\"\n[programs.web.environment]\nKAHI_RLIMIT_NOFILE = \"notanumber\"\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runKahiEnv(t, dir, searchEnv(), "daemon", "-c", cfg)
	if want := "programs.web: invalid rlimit KAHI_RLIMIT_NOFILE: notanumber"; code != 1 || !strings.Contains(stderr, want) {
		t.Fatalf("daemon: exit %d, stderr %q; want exit 1 and %q", code, stderr, want)
	}
}

// A child whose user differs from the daemon's starts from a minimal
// environment: PATH, HOME from its passwd entry ("/" if it has none),
// SUPERVISOR_* and its own entries. inherit_environment opts back in; a child
// without a user inherits as before (FUNC-016, SEC-021).
func TestRootContainer_CleanEnvForOtherUser(t *testing.T) {
	t.Parallel()
	c := startRootContainer(t, map[string]string{
		"etc/passwd": "root:x:0:0:root:/root:/sbin/nologin\nnobody:x:65534:65534:nobody:/home/nobody:/sbin/nologin\n",
		"kahi.toml": `[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"

[programs.switched]
command = "/orphaner env"
user = "65534:65534"
startsecs = 0
autorestart = "false"
[programs.switched.environment]
APP_MODE = "prod"

[programs.optin]
command = "/orphaner env"
user = "65534:65534"
inherit_environment = true
startsecs = 0
autorestart = "false"

[programs.nohome]
command = "/orphaner env"
user = "4242:4242"
startsecs = 0
autorestart = "false"

[programs.same]
command = "/orphaner env"
startsecs = 0
autorestart = "false"
`,
	}, []string{"-e", "ROOT_SECRET=topsecret", "-e", "PATH=/usr/bin:/bin"}, nil)
	c.waitLogs(`"msg":"supervisor running"`)

	env := func(program string) []string {
		c.waitOutput(program, "SUPERVISOR_PROCESS_NAME=")
		lines := c.output(program)
		sort.Strings(lines)
		return lines
	}
	want := []string{
		"APP_MODE=prod",
		"HOME=/home/nobody",
		"PATH=/usr/bin:/bin",
		"SUPERVISOR_ENABLED=1",
		"SUPERVISOR_GROUP_NAME=switched",
		"SUPERVISOR_PROCESS_NAME=switched",
	}
	if got := env("switched"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("switched child environment:\n%s\nwant exactly:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := strings.Join(env("optin"), "\n"); !strings.Contains(got, "ROOT_SECRET=topsecret") {
		t.Errorf("inherit_environment child lacks ROOT_SECRET:\n%s", got)
	}
	if got := env("nohome"); !contains(got, "HOME=/") || strings.Contains(strings.Join(got, "\n"), "ROOT_SECRET") {
		t.Errorf("child of a uid without a passwd entry:\n%s\nwant HOME=/ and no ROOT_SECRET", strings.Join(got, "\n"))
	}
	if got := strings.Join(env("same"), "\n"); !strings.Contains(got, "ROOT_SECRET=topsecret") {
		t.Errorf("child without a user does not inherit the daemon environment:\n%s", got)
	}
}
