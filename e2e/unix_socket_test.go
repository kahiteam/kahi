//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// The Unix socket server (FUNC-024, SEC-022, #144).

// maxSocketPath mirrors the daemon's limit: sun_path less its NUL.
func maxSocketPath() int {
	if runtime.GOOS == "linux" {
		return 107
	}
	return 103
}

// socketDaemon writes a config with [server.unix] lines and starts the
// daemon; it returns the command and a channel with its exit.
func socketDaemon(t *testing.T, dir, unix string) (*exec.Cmd, <-chan error) {
	t.Helper()
	cfg := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(cfg, []byte("[supervisor]\nshutdown_timeout = 5\n\n[server.unix]\n"+unix+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := startDaemonCmd(t, kahiBinary, cfg, dir)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd, done
}

func socketRefused(t *testing.T, unix, want string) {
	t.Helper()
	dir := shortTempDir(t)
	cfg := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(cfg, []byte("[server.unix]\n"+unix+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runKahi(t, dir, "daemon", "-c", cfg)
	if code == 0 || !strings.Contains(stderr, want) {
		t.Fatalf("[server.unix] %s: exit %d, stderr %q; want %q", unix, code, stderr, want)
	}
}

// A path of exactly the platform limit works; one byte more is a config
// error naming the path.
func TestUnixSocket_PathLimit(t *testing.T) {
	dir := shortTempDir(t)
	limit := maxSocketPath()
	pad := limit - len(dir) - 1
	if pad < 1 {
		t.Skipf("temp dir %s leaves no room", dir)
	}
	exact := filepath.Join(dir, strings.Repeat("s", pad))
	socketDaemon(t, dir, fmt.Sprintf("file = %q", exact))
	waitForSocket(t, exact, 5*time.Second)
	waitForHealth(t, ctl.NewUnixClient(exact), 3*time.Second)

	long := exact + "x"
	socketRefused(t, fmt.Sprintf("file = %q", long), "socket path too long: "+long)
}

// chmod sets the socket's mode; anything group or others could use, and
// chown, are refused (SEC-022).
func TestUnixSocket_ModeAndOwnership(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "kahi.sock")
	socketDaemon(t, dir, fmt.Sprintf("file = %q\nchmod = \"0600\"", sock))
	waitForSocket(t, sock, 5*time.Second)
	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o600 || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket %v, %v; want a socket with mode 0600", fi, err)
	}
	socketRefused(t, `chmod = "0770"`, "control socket must be owner-only (0700); got 0770")
	socketRefused(t, `chown = "root:wheel"`, "server.unix.chown is not supported; socket is owner-only")
}

// A socket file left by a crashed daemon is replaced; graceful shutdown
// removes the socket; a SIGKILLed daemon leaves it, and the next start
// replaces it.
func TestUnixSocket_StaleAndCleanup(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "kahi.sock")

	// A dead socket: bound, closed, not unlinked.
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("no stale socket to start from: %v", err)
	}

	_, done := socketDaemon(t, dir, fmt.Sprintf("file = %q", sock))
	client := ctl.NewUnixClient(sock)
	waitForHealth(t, client, 5*time.Second)
	if err := client.Shutdown(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not exit")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket still there after graceful shutdown: %v", err)
	}

	cmd, done := socketDaemon(t, dir, fmt.Sprintf("file = %q", sock))
	waitForHealth(t, client, 5*time.Second)
	_ = cmd.Process.Signal(syscall.SIGKILL)
	<-done
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("SIGKILL removed the socket: %v", err)
	}
	socketDaemon(t, dir, fmt.Sprintf("file = %q", sock))
	waitForHealth(t, client, 5*time.Second)
}
