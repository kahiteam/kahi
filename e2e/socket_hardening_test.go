//go:build e2e

package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// Socket directory probe (SEC-024) and owner-only socket creation (SEC-023),
// as the daemon binary shows them.

// socketConfig writes a config with [server.unix] file = socket (and the
// programs) to dir/kahi.toml and returns its path.
func socketConfig(t *testing.T, dir, socket, programs string) string {
	t.Helper()
	cfg := filepath.Join(dir, "kahi.toml")
	body := fmt.Sprintf("[supervisor]\nshutdown_timeout = 5\n\n[server.unix]\nfile = %q\n\n%s", socket, programs)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// The write probe never opens an existing path: a .kahi_perm_check symlink
// planted in the socket directory keeps its target intact, and no probe
// file is left behind.
func TestSocketProbe_DoesNotFollowSymlink(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".kahi_perm_check")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "kahi.sock")
	cmd := startDaemonCmd(t, kahiBinary, socketConfig(t, dir, socket, ""), dir)
	t.Cleanup(func() { stopDaemon(t, cmd, socket) })
	waitForSocket(t, socket, 5*time.Second)
	waitForHealth(t, ctl.NewUnixClient(socket), 3*time.Second)

	if b, _ := os.ReadFile(target); string(b) != "keep\n" {
		t.Errorf("symlink target now %q, want it untouched", b)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the planted link is no longer a symlink: %v %v", fi, err)
	}
	if probes, _ := filepath.Glob(filepath.Join(dir, ".kahi_perm_check*")); len(probes) != 1 {
		t.Errorf("probe files left: %v, want only the planted link", probes)
	}
}

// A socket directory the daemon cannot write to stops it with a clear
// error; a bare socket name resolves against the daemon's working directory.
func TestSocketProbe_UnwritableAndRelative(t *testing.T) {
	t.Parallel()
	t.Run("unwritable", func(t *testing.T) {
		t.Parallel()
		if os.Geteuid() == 0 {
			t.Skip("root writes to a 0555 directory")
		}
		dir := shortDir(t)
		sockDir := filepath.Join(dir, "ro")
		if err := os.Mkdir(sockDir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(sockDir, 0o755) })
		cfg := socketConfig(t, dir, filepath.Join(sockDir, "kahi.sock"), "")
		_, stderr, code := runKahi(t, dir, "daemon", "-c", cfg)
		if want := "permission denied: cannot create socket in " + sockDir; code != 1 || !strings.Contains(stderr, want) {
			t.Fatalf("exit %d, stderr %q; want exit 1 and %q", code, stderr, want)
		}
	})
	t.Run("relative", func(t *testing.T) {
		t.Parallel()
		dir := shortDir(t)
		cmd := startDaemonCmd(t, kahiBinary, socketConfig(t, dir, "kahi.sock", ""), dir)
		socket := filepath.Join(dir, "kahi.sock")
		t.Cleanup(func() { stopDaemon(t, cmd, socket) })
		waitForSocket(t, socket, 5*time.Second)
		waitForHealth(t, ctl.NewUnixClient(socket), 3*time.Second)
	})
}

// Started under umask 000, the daemon still creates its socket owner-only,
// and the umask its programs inherit is the one it started with: the
// temporary umask around bind is restored (SEC-023).
func TestSocketBind_OwnerOnlyUnderOpenUmask(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	socket := filepath.Join(dir, "kahi.sock")
	script := writeScript(t, dir, "umask.sh", "umask > umask.out\nexec sleep 300")
	cfg := socketConfig(t, dir, socket, "[programs.web]\ncommand = \""+script+"\"\nstartsecs = 0\nstopwaitsecs = 2\n")
	cmd := exec.Command("/bin/sh", "-c", "umask 000 && exec \"$0\" daemon -c \"$1\"", kahiBinary, cfg)
	cmd.Dir = dir
	out := daemonOutput(t)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopDaemon(t, cmd, socket) })
	waitForSocket(t, socket, 5*time.Second)
	if fi, err := os.Stat(socket); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("socket %v %v; want mode 0700 under umask 000", fi, err)
	}
	waitForMarker(t, filepath.Join(dir, "umask.out"), "0000", 5*time.Second)
}

// The configured mode is applied at bind, with no chmod by path afterwards
// (decided 2026-11-20): under a stricter inherited umask the socket still
// comes out exactly as configured.
func TestSocketBind_ConfiguredModeUnderStrictUmask(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	socket := filepath.Join(dir, "kahi.sock")
	cfg := filepath.Join(dir, "kahi.toml")
	body := fmt.Sprintf("[supervisor]\nshutdown_timeout = 5\n\n[server.unix]\nfile = %q\nchmod = \"0600\"\n", socket)
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", "umask 0277 && exec \"$0\" daemon -c \"$1\"", kahiBinary, cfg)
	cmd.Dir = dir
	out := daemonOutput(t)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopDaemon(t, cmd, socket) })
	waitForSocket(t, socket, 5*time.Second)
	if fi, err := os.Lstat(socket); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket %v %v; want mode 0600 under umask 0277", fi, err)
	}
}

// A symlink at the socket path is refused at start, even when it points at a
// stale socket: it is not judged by its target (Lstat, decided 2026-11-20).
func TestSocketBind_SymlinkAtSocketPathRefused(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	stale := filepath.Join(dir, "stale.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	socket := filepath.Join(dir, "kahi.sock")
	if err := os.Symlink(stale, socket); err != nil {
		t.Fatal(err)
	}
	cfg := socketConfig(t, dir, socket, "")
	_, stderr, code := runKahi(t, dir, "daemon", "-c", cfg)
	if want := "cannot create socket: " + socket + ": " + socket + " exists and is not a socket"; code == 0 || !strings.Contains(stderr, want) {
		t.Fatalf("exit %d, stderr %q; want %q", code, stderr, want)
	}
	if fi, err := os.Lstat(socket); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the planted link was replaced: %v %v", fi, err)
	}
}
