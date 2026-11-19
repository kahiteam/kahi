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

// kahi config check follow-ups from the v0.3.0 review (FUNC-087).

// An existing per-user socket directory the daemon cannot create files in
// fails the check with the daemon's own text.
func TestConfigCheck_PrivateSocketDirNotWritable(t *testing.T) {
	skipCheckAsRoot(t)
	tmp := shortTempDir(t)
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", tmp)
	sockDir := filepath.Join(tmp, fmt.Sprintf("kahi-%d", os.Geteuid()))
	if err := os.Mkdir(sockDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sockDir, 0o700) })
	cfgPath := filepath.Join(tmp, "kahi.toml")
	if err := os.WriteFile(cfgPath, []byte("[programs.web]\ncommand = \"/bin/sleep 300\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := "permission denied: cannot create socket in " + sockDir
	if _, stderr, code := runKahi(t, tmp, "daemon", "-c", cfgPath); code == 0 || !strings.Contains(stderr, want) {
		t.Fatalf("daemon: exit %d, stderr %q; want %q", code, stderr, want)
	}
	stdout, stderr, code := runKahi(t, tmp, "config", "check", "-c", cfgPath)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "error: "+want) {
		t.Errorf("check: exit %d, stdout %q, stderr %q; want error: %s", code, stdout, stderr, want)
	}
}

// When the [supervisor] directory chdir fails, relative program paths
// would resolve elsewhere than in the daemon: they are skipped, and only the
// directory error is reported.
func TestConfigCheck_RelativeProgramPathsSkippedWithoutDirectory(t *testing.T) {
	t.Parallel()
	base := realDir(t)
	dir, path := checkConfig(t, fmt.Sprintf("[supervisor]\ndirectory = %q\n\n[programs.web]\ncommand = \"bin/web\"\nstdout_logfile = \"logs/web.log\"\n\n[programs.job]\ncommand = \"/bin/sleep 300\"\ndirectory = \"work\"\n",
		filepath.Join(base, "nodir")))
	_, stderr, code := runKahi(t, dir, "config", "check", "-c", path)
	if code != 1 || !strings.Contains(stderr, "error: directory does not exist: "+filepath.Join(base, "nodir")) {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
	if strings.Contains(stderr, "warning: programs.") {
		t.Errorf("relative program paths probed from the wrong directory:\n%s", stderr)
	}
}

// A bare command run through the trampoline (umask) with a relative PATH
// entry runs from the program's directory (exec.LookPath's ErrDot leaves the
// bare name): the check finds it there, and the daemon starts it.
func TestConfigCheck_TrampolineRelativePathEntry(t *testing.T) {
	t.Parallel()
	base := realDir(t)
	app := filepath.Join(base, "app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, app, "worker", "exec /bin/sleep 300\n")
	body := fmt.Sprintf("[programs.w]\ncommand = \"worker\"\ndirectory = %q\numask = \"022\"\nstartsecs = 0\nenvironment = { PATH = \"/nonexistent:\" }\n", app)
	dir, path := checkConfig(t, body)
	_, stderr, code := runKahi(t, dir, "config", "check", "-c", path)
	if code != 0 || strings.Contains(stderr, "programs.w.command") {
		t.Errorf("check: code %d, stderr %q", code, stderr)
	}
	client, _ := startDaemon(t, body)
	waitForState(t, client, "w", "RUNNING", 5*time.Second)
}

// Values with spaces are quoted as in a text log.
func TestConfigCheck_WarningValuesQuoted(t *testing.T) {
	t.Parallel()
	dir, path := checkConfig(t, "[programs.web]\ncommand = \"/bin/sleep 300\"\n")
	pid := filepath.Join(dir, "stale pid")
	if err := os.WriteFile(pid, []byte("12 34\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, _ := runKahi(t, dir, "config", "check", "-c", path, "-p", pid)
	if want := fmt.Sprintf("warning: replacing stale PID file path=%q content=\"12 34\"\n", pid); !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
}

// Run as root, the program checks use root's access rights; the programs
// start as the drop user or their own user, so the check says what it did
// not cover.
func TestRootContainer_ConfigCheckProgramAccessNote(t *testing.T) {
	t.Parallel()
	c := startRootContainer(t, usersImage(`[supervisor]
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"

[programs.web]
command = "/orphaner id"
user = "33"
startsecs = 0
autorestart = false
`), nil, nil)
	c.waitLogs(`"msg":"supervisor running"`)
	check := func(args ...string) string {
		out, _ := exec.Command(c.rt, append([]string{"exec", c.name, "/kahi", "config", "check", "-c", "/kahi.toml"}, args...)...).CombinedOutput()
		return string(out)
	}
	if out, want := check("-u", "kahi"), "note: program checks ran as root; after the privilege drop the daemon opens program logs and starts programs as kahi (uid 1000), whose access was not checked\n"; !strings.Contains(out, want) {
		t.Errorf("with -u kahi: %q lacks %q", out, want)
	}
	if out, want := check(), "note: program checks ran as root; a program with a user setting starts as that user, whose access to its command and directory was not checked\n"; !strings.Contains(out, want) {
		t.Errorf("program user: %q lacks %q", out, want)
	}
}
