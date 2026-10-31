//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// containerRuntime returns podman or docker if one answers, or skips. On a
// Linux CI runner a missing runtime fails the test instead, so the PID 1
// proof cannot silently drop out of CI.
func containerRuntime(t *testing.T) string {
	t.Helper()
	for _, rt := range []string{"podman", "docker"} {
		if _, err := exec.LookPath(rt); err == nil && exec.Command(rt, "info").Run() == nil {
			return rt
		}
	}
	if os.Getenv("GITHUB_ACTIONS") == "true" && runtime.GOOS == "linux" {
		t.Fatal("no working podman or docker on the CI runner")
	}
	t.Skip("no running podman or docker")
	return ""
}

// As PID 1 of a FROM scratch container, the way the release image runs it,
// Kahi reaps orphaned processes it did not start, and logs them as expected
// orphans instead of warnings (FUNC-065, #145).
func TestPID1_ReapsOrphans(t *testing.T) {
	rt := containerRuntime(t)
	dir := t.TempDir()
	build := func(out, pkg string) {
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, out), pkg)
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", pkg, err, b)
		}
	}
	build("kahi", "github.com/kahiteam/kahi/cmd/kahi")
	build("orphaner", "./testdata/orphaner")

	files := map[string]string{
		"kahi.toml": `[supervisor]
log_level = "debug"
shutdown_timeout = 5

[server.unix]
file = "/run/kahi.sock"

[programs.orphaner]
command = "/orphaner spawn 3 kill"
startsecs = 0
autorestart = "false"
`,
		"Containerfile": `FROM scratch
COPY kahi orphaner kahi.toml /
COPY run /run
ENTRYPOINT ["/kahi", "daemon", "-c", "/kahi.toml"]
`,
		"run/.keep": "",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tag := fmt.Sprintf("localhost/kahi-pid1-e2e:%d", time.Now().UnixNano())
	name := fmt.Sprintf("kahi-pid1-e2e-%d", time.Now().UnixNano())
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(rt, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", rt, strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	run("build", "-q", "-t", tag, "-f", filepath.Join(dir, "Containerfile"), dir)
	t.Cleanup(func() { _ = exec.Command(rt, "rmi", "-f", tag).Run() })
	run("run", "-d", "--name", name, tag)
	t.Cleanup(func() { _ = exec.Command(rt, "rm", "-f", name).Run() })

	// The three children outlive orphaner by a second each; Kahi, as their
	// new parent, reaps them.
	var logs string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		logs = run("logs", name)
		if strings.Count(logs, `"msg":"reaped orphan"`) >= 3 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if n := strings.Count(logs, `"msg":"reaped orphan"`); n != 3 {
		t.Fatalf("reaped %d orphans, want 3; logs:\n%s", n, logs)
	}
	// The orphans end by SIGKILL; the log names it as in <signal.h> (#215).
	if n := strings.Count(logs, `"signal":"SIGKILL"`); n != 3 {
		t.Errorf("%d reaped-orphan lines name SIGKILL, want 3; logs:\n%s", n, logs)
	}
	for _, want := range []string{"running as PID 1; orphaned processes are reaped", `"process":"orphaner"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs lack %q", want)
		}
	}
	if strings.Contains(logs, "reaped unknown child") {
		t.Errorf("orphans logged as unknown children:\n%s", logs)
	}
	if out := strings.TrimSpace(run("exec", name, "/orphaner", "zombies")); out != "0" {
		t.Errorf("%s zombie processes in the container, want 0", out)
	}

	// Stopping the container sends SIGTERM to PID 1: Kahi shuts down
	// cleanly, logging to stdout in the foreground.
	run("stop", "-t", "10", name)
	if code := strings.TrimSpace(run("inspect", "-f", "{{.State.ExitCode}}", name)); code != "0" {
		t.Errorf("container exit code %s, want 0", code)
	}
	if logs = run("logs", name); !strings.Contains(logs, `"msg":"shutdown complete"`) || !strings.Contains(logs, `"msg":"supervisor running"`) {
		t.Errorf("logs lack the startup or clean shutdown:\n%s", logs)
	}
}

// In the foreground, SIGINT (Ctrl+C) shuts the daemon down gracefully.
func TestForeground_SIGINTShutsDown(t *testing.T) {
	cmd, _, done, _, _ := shutdownDaemon(t, 10, `
[programs.worker]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon exit after SIGINT: %v, want 0", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon still running 10s after SIGINT")
	}
}

// Not as PID 1, Kahi's grandchildren are reparented elsewhere: Kahi reaps
// only its own children and reports no unknown ones.
func TestPID1_NotPID1ReapsOwnChildrenOnly(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "orphaner"), "./testdata/orphaner")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build orphaner: %v\n%s", err, b)
	}
	logDir := shortTempDir(t)
	log := filepath.Join(logDir, "kahi.log")
	sock := filepath.Join(logDir, "kahi.sock")
	cfgPath := filepath.Join(logDir, "kahi.toml")
	cfg := fmt.Sprintf(`[supervisor]
logfile = %q
log_level = "debug"
shutdown_timeout = 5

[server.unix]
file = %q

[programs.orphaner]
command = "%s spawn 3"
startsecs = 0
autorestart = "false"
`, log, sock, filepath.Join(dir, "orphaner"))
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	daemon := startDaemonCmd(t, kahiBinary, cfgPath, logDir)
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	waitForSocket(t, sock, 5*time.Second)
	time.Sleep(2500 * time.Millisecond) // the grandchildren exit after 1s

	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), `"process":"orphaner"`) || strings.Contains(string(b), "reaped unknown child") ||
		strings.Contains(string(b), "reaped orphan") || strings.Contains(string(b), "running as PID 1") {
		t.Fatalf("daemon log:\n%s", b)
	}
}
