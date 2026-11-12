//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Root-only behaviour runs as root in a FROM scratch container (ADR-037):
// the test user is not root, and the tests must never need sudo. Like
// TestPID1_ReapsOrphans they skip without a running podman or docker and
// fail on a Linux CI runner without one.

var (
	linuxBinOnce sync.Once
	linuxBinDir  string
	linuxBinErr  error
)

// linuxBinaries builds kahi and orphaner for Linux once per suite and
// returns their directory.
func linuxBinaries(t *testing.T) string {
	t.Helper()
	linuxBinOnce.Do(func() {
		linuxBinDir = filepath.Join(filepath.Dir(kahiBinary), "linux")
		if linuxBinErr = os.MkdirAll(linuxBinDir, 0o755); linuxBinErr != nil {
			return
		}
		for out, pkg := range map[string]string{"kahi": "github.com/kahiteam/kahi/cmd/kahi", "orphaner": "./testdata/orphaner"} {
			cmd := exec.Command("go", "build", "-o", filepath.Join(linuxBinDir, out), pkg)
			cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
			if b, err := cmd.CombinedOutput(); err != nil {
				linuxBinErr = fmt.Errorf("build %s: %v\n%s", pkg, err, b)
				return
			}
		}
	})
	if linuxBinErr != nil {
		t.Fatal(linuxBinErr)
	}
	return linuxBinDir
}

// rootContainer is a running container whose PID 1 is "kahi daemon -c
// /kahi.toml", as root.
type rootContainer struct {
	t        *testing.T
	rt, name string
}

// startRootContainer builds an image with kahi, orphaner, /run and the
// given files (image path → content, kahi.toml among them) and starts it.
// runArgs go to "run" before the image (e.g. -e), daemonArgs after it, to
// kahi daemon (e.g. -u 65534). The container and image are removed at
// cleanup.
func startRootContainer(t *testing.T, files map[string]string, runArgs, daemonArgs []string) *rootContainer {
	t.Helper()
	rt := containerRuntime(t)
	bin := linuxBinaries(t)
	dir := t.TempDir()
	for _, name := range []string{"kahi", "orphaner"} {
		copyFile(t, filepath.Join(bin, name), filepath.Join(dir, name), 0o755)
	}
	files["Containerfile"] = "FROM scratch\nCOPY . /\nENTRYPOINT [\"/kahi\", \"daemon\", \"-c\", \"/kahi.toml\"]\n"
	files["run/.keep"] = ""
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := &rootContainer{t: t, rt: rt, name: fmt.Sprintf("kahi-root-e2e-%d", time.Now().UnixNano())}
	tag := "localhost/" + c.name
	c.run("build", "-q", "-t", tag, "-f", filepath.Join(dir, "Containerfile"), dir)
	t.Cleanup(func() { _ = exec.Command(rt, "rmi", "-f", tag).Run() })
	c.run(append(append(append([]string{"run", "-d", "--name", c.name}, runArgs...), tag), daemonArgs...)...)
	t.Cleanup(func() { _ = exec.Command(rt, "rm", "-f", c.name).Run() })
	return c
}

func copyFile(t *testing.T, src, dst string, mode os.FileMode) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// run runs the container runtime with args and returns its output.
func (c *rootContainer) run(args ...string) string {
	c.t.Helper()
	out, err := exec.Command(c.rt, args...).CombinedOutput()
	if err != nil {
		c.t.Fatalf("%s %s: %v\n%s", c.rt, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// logs returns the container's output: the daemon's JSON log lines and its
// programs' output as JSON lines.
func (c *rootContainer) logs() string {
	c.t.Helper()
	return c.run("logs", c.name)
}

// waitLogs waits until the logs contain want and returns them.
func (c *rootContainer) waitLogs(want string) string {
	c.t.Helper()
	var logs string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if logs = c.logs(); strings.Contains(logs, want) {
			return logs
		}
	}
	c.t.Fatalf("container logs lack %q after 20s:\n%s", want, logs)
	return ""
}

// output returns the lines program printed, from its JSON output lines.
func (c *rootContainer) output(program string) []string {
	c.t.Helper()
	var lines []string
	for _, line := range strings.Split(c.logs(), "\n") {
		var l struct{ Process, Stream, Log string }
		if json.Unmarshal([]byte(line), &l) == nil && l.Process == program && l.Stream != "" {
			lines = append(lines, l.Log)
		}
	}
	return lines
}

// waitOutput waits until program has printed a line with prefix and
// returns that line.
func (c *rootContainer) waitOutput(program, prefix string) string {
	c.t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		for _, l := range c.output(program) {
			if strings.HasPrefix(l, prefix) {
				return l
			}
		}
	}
	c.t.Fatalf("%s printed no %q line after 20s; logs:\n%s", program, prefix, c.logs())
	return ""
}

const (
	rootWarning    = "running as root without user config"
	rootWarningUse = "consider kahi daemon -u "
)

// As root without -u, the daemon warns exactly once, naming -u; a reload
// does not warn again. A program with user = "65534:65534" runs as that
// user while the daemon stays root (FUNC-048, FUNC-047, #248).
func TestRootContainer_WarningOnceAndUserSwitch(t *testing.T) {
	t.Parallel()
	c := startRootContainer(t, map[string]string{"kahi.toml": `[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"

[programs.rootid]
command = "/orphaner id"
startsecs = 0
autorestart = "false"

[programs.userid]
command = "/orphaner id"
user = "65534:65534"
startsecs = 0
autorestart = "false"
`}, nil, nil)
	c.waitLogs(`"msg":"supervisor running"`)
	if got := c.waitOutput("rootid", "uid="); !strings.HasPrefix(got, "uid=0 euid=0 gid=0 egid=0 ") {
		t.Errorf("rootid: %q, want uid 0", got)
	}
	if got := c.waitOutput("userid", "uid="); got != "uid=65534 euid=65534 gid=65534 egid=65534 groups=[]" {
		t.Errorf("userid: %q, want uid and gid 65534 with no supplementary groups", got)
	}
	logs := c.logs()
	if n := strings.Count(logs, rootWarning); n != 1 || !strings.Contains(logs, rootWarningUse) {
		t.Fatalf("%d root warnings, want 1 naming -u; logs:\n%s", n, logs)
	}
	if strings.Contains(logs, "consider using a non-root user") || strings.Contains(logs, "[supervisor] user") {
		t.Errorf("logs carry an obsolete root warning:\n%s", logs)
	}

	c.run("kill", "-s", "HUP", c.name)
	logs = c.waitLogs(`"msg":"config reloaded"`)
	if n := strings.Count(logs, rootWarning); n != 1 {
		t.Errorf("%d root warnings after SIGHUP, want still 1; logs:\n%s", n, logs)
	}
}

// With -u the daemon drops to that user, does not warn, and its programs
// run as the user too (FUNC-048, privilege dropping).
func TestRootContainer_UserFlagNoWarning(t *testing.T) {
	t.Parallel()
	c := startRootContainer(t, map[string]string{"kahi.toml": `[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"

[programs.id]
command = "/orphaner id"
startsecs = 0
autorestart = "false"
`}, nil, []string{"-u", "65534"})
	logs := c.waitLogs(`"msg":"dropped privileges"`)
	if !strings.Contains(logs, `"uid":65534`) {
		t.Errorf("privilege drop not to uid 65534; logs:\n%s", logs)
	}
	if got := c.waitOutput("id", "uid="); !strings.HasPrefix(got, "uid=65534 euid=65534 gid=65534 egid=65534 ") {
		t.Errorf("id: %q, want uid and gid 65534", got)
	}
	if logs = c.logs(); strings.Contains(logs, rootWarning) {
		t.Errorf("root warning with -u; logs:\n%s", logs)
	}
}

// With -d only the daemonized child, which runs the supervisor, warns; the
// foreground parent does not (FUNC-048, #209, #248).
func TestRootContainer_DaemonizeWarnsOnce(t *testing.T) {
	t.Parallel()
	c := startRootContainer(t, map[string]string{
		"kahi.toml": `[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"

[programs.inner]
command = "/kahi daemon -d -c /inner.toml"
startsecs = 0
autorestart = "false"
`,
		"inner.toml": `[supervisor]
logfile = "/run/inner.log"
shutdown_timeout = 5

[server.unix]
file = "/run/inner.sock"
`,
	}, nil, nil)
	c.waitLogs(`"msg":"supervisor running"`)
	var inner string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if out, err := exec.Command(c.rt, "exec", c.name, "/orphaner", "cat", "/run/inner.log").CombinedOutput(); err == nil {
			if inner = string(out); strings.Contains(inner, `"msg":"supervisor running"`) {
				break
			}
		}
	}
	if !strings.Contains(inner, `"msg":"supervisor running"`) {
		t.Fatalf("inner daemon log never showed it running:\n%s\nouter logs:\n%s", inner, c.logs())
	}
	if n := strings.Count(inner, rootWarning); n != 1 {
		t.Errorf("%d root warnings in the -d daemon's log, want 1 (the child only):\n%s", n, inner)
	}
	for _, l := range c.output("inner") {
		if strings.Contains(l, rootWarning) {
			t.Errorf("the -d parent warned: %q", l)
		}
	}
}

// Not as root, there is no root warning (FUNC-048).
func TestRootWarning_NotRoot(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	_, _, out := logDaemon(t, shortDir(t), "", "")
	waitFor(t, "daemon stdout", out.String, "supervisor running")
	if strings.Contains(out.String(), "running as root") {
		t.Fatalf("root warning as uid %d:\n%s", os.Geteuid(), out.String())
	}
}
