//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/kahiteam/kahi/internal/ctl"
	"github.com/kahiteam/kahi/internal/testutil"
)

// kahi daemon -d and the PID file (FUNC-064, #143).

func pidfileConfig(t *testing.T, dir, extra string) (cfgPath, sock string) {
	t.Helper()
	sock = filepath.Join(dir, "kahi.sock")
	cfgPath = filepath.Join(dir, "kahi.toml")
	cfg := fmt.Sprintf("[supervisor]\nshutdown_timeout = 5\nlogfile = %q\n%s\n[server.unix]\nfile = %q\n\n[programs.sleeper]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\n",
		filepath.Join(dir, "kahi.log"), extra, sock)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath, sock
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("PID file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("PID file %q: %v", b, err)
	}
	return pid
}

// -d returns once the daemon runs: it is in its own session, its PID file
// holds its pid, it serves the socket, and a shutdown removes the PID file.
func TestDaemonize_StartsInBackground(t *testing.T) {
	dir := shortTempDir(t)
	cfg, sock := pidfileConfig(t, dir, "")
	pidfile := filepath.Join(dir, "kahi.pid")

	start := time.Now()
	out, stderr, code := runKahi(t, dir, "daemon", "-d", "-c", cfg, "-p", pidfile)
	if code != 0 || !strings.HasPrefix(out, "kahi daemon started (pid ") {
		t.Fatalf("daemon -d: exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	pid := readPID(t, pidfile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if !strings.Contains(out, fmt.Sprintf("(pid %d)", pid)) {
		t.Errorf("stdout %q does not name the PID file's pid %d", out, pid)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("daemon -d took %v to return", time.Since(start))
	}

	// Its own session, detached from the test's.
	sid, err := unix.Getsid(pid)
	if err != nil || sid != pid {
		t.Errorf("daemon session %d (%v), want its own (%d)", sid, err, pid)
	}
	client := ctl.NewUnixClient(sock)
	waitForHealth(t, client, 3*time.Second)
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)
	if got := daemonPID(t, client); got != pid {
		t.Errorf("daemon reports pid %d, PID file %d", got, pid)
	}

	if _, stderr, code := runKahi(t, dir, "ctl", "-s", sock, "shutdown"); code != 0 {
		t.Fatalf("ctl shutdown: exit %d, %q", code, stderr)
	}
	if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
		t.Errorf("PID file still there after shutdown: %v", err)
	}
	// The socket goes before the process exits and is reaped.
	waitForDeath(t, pid, 5*time.Second)
}

// When the background daemon fails to start, -d reports its error and exits 1.
func TestDaemonize_ReportsStartupError(t *testing.T) {
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreeTCPPort(t))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	dir := shortTempDir(t)
	cfg, _ := pidfileConfig(t, dir, "")
	b, _ := os.ReadFile(cfg)
	httpTable := fmt.Sprintf("\n[server.http]\nenabled = true\nlisten = %q\nusername = \"admin\"\n%s = %q\n", addr, "pass"+"word", hashPassword(t, "secret"))
	if err := os.WriteFile(cfg, append(b, httpTable...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runKahi(t, dir, "daemon", "-d", "-c", cfg)
	if code != 1 || !strings.Contains(stderr, "cannot bind "+addr) {
		t.Fatalf("daemon -d with the port in use: exit %d, stderr %q", code, stderr)
	}
}

// A PID file naming a live process refuses the start; a stale one is
// replaced with a warning; an unwritable one is an error.
func TestDaemonize_PIDFileChecks(t *testing.T) {
	dir := shortTempDir(t)
	cfg, sock := pidfileConfig(t, dir, "")
	pidfile := filepath.Join(dir, "kahi.pid")

	// Live: this test's own process.
	if err := os.WriteFile(pidfile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runKahi(t, dir, "daemon", "-c", cfg, "-p", pidfile)
	if code != 1 || !strings.Contains(stderr, fmt.Sprintf("another instance running (pid %d)", os.Getpid())) {
		t.Fatalf("live PID file: exit %d, stderr %q", code, stderr)
	}

	// Stale: a process that has exited.
	dead := exec.Command("/bin/sh", "-c", "exit 0")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidfile, []byte(fmt.Sprintf("%d\n", dead.ProcessState.Pid())), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := startDaemonCmd(t, kahiBinary, cfg, dir, "-p", pidfile)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitForSocket(t, sock, 5*time.Second)
	waitForHealth(t, ctl.NewUnixClient(sock), 3*time.Second)
	if got := readPID(t, pidfile); got != cmd.Process.Pid {
		t.Errorf("stale PID file holds %d, want the new daemon %d", got, cmd.Process.Pid)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "kahi.log"))
	if !strings.Contains(string(log), `"msg":"replacing stale PID file"`) {
		t.Errorf("no stale PID file warning in the log:\n%s", log)
	}

	// Unwritable.
	if os.Geteuid() != 0 {
		dir2 := shortTempDir(t)
		cfg2, _ := pidfileConfig(t, dir2, "")
		locked := filepath.Join(dir2, "locked")
		if err := os.Mkdir(locked, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
		_, stderr, code = runKahi(t, dir2, "daemon", "-c", cfg2, "-p", filepath.Join(locked, "kahi.pid"))
		if code != 1 || !strings.Contains(stderr, "cannot write PID file: "+filepath.Join(locked, "kahi.pid")) {
			t.Fatalf("unwritable PID file: exit %d, stderr %q", code, stderr)
		}
	}
}
