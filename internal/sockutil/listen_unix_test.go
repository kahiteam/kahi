//go:build unix

package sockutil

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// These tests change the process umask, which is process-wide state, so none of
// them may run in parallel. Each restores the umask it found.

// currentUmask reads the umask. POSIX has no read-only accessor, so it sets a
// value and immediately puts the original back.
func currentUmask() int {
	m := syscall.Umask(0)
	syscall.Umask(m)
	return m
}

// withUmask runs the test under mask and restores the original afterwards.
func withUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

// socketPath returns a socket path under a short temp dir. Unix socket paths
// are limited (~104 bytes on darwin), and t.TempDir() embeds the long test name.
func socketPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

func listen(t *testing.T, path string) {
	t.Helper()
	ln, err := ListenUnix(path, 0o700)
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
}

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// Under a fully permissive umask a bare net.Listen creates the socket 0777.
// ListenUnix must create it owner-only, with no chmod involved.
func TestListenUnixOwnerOnlyUnderPermissiveUmask(t *testing.T) {
	withUmask(t, 0)
	p := socketPath(t, "s.sock")
	listen(t, p)

	if got := perm(t, p); got != 0o700 {
		t.Fatalf("socket created %#o under umask 000, want 0700", got)
	}
}

func TestListenUnixRestoresUmaskOnSuccess(t *testing.T) {
	withUmask(t, 0o022)
	listen(t, socketPath(t, "s.sock"))

	if got := currentUmask(); got != 0o022 {
		t.Fatalf("umask after ListenUnix = %#o, want 0022", got)
	}
}

func TestListenUnixRestoresUmaskOnBindFailure(t *testing.T) {
	withUmask(t, 0o022)
	missing := filepath.Join(t.TempDir(), "missing-dir", "s.sock")

	ln, err := ListenUnix(missing, 0o700)
	if err == nil {
		_ = ln.Close()
		t.Fatal("ListenUnix into a missing directory must fail")
	}
	if got := currentUmask(); got != 0o022 {
		t.Fatalf("umask after failed ListenUnix = %#o, want 0022", got)
	}
}

// The configured mode is exact: a stricter inherited umask no longer narrows
// it (no chmod corrects it afterwards), but group and other bits are never
// granted, whatever the mode or the inherited umask says.
func TestListenUnixAppliesModeExactly(t *testing.T) {
	for _, tc := range []struct {
		inherited int
		mode      os.FileMode
		want      os.FileMode
	}{
		{0o277, 0o700, 0o700},
		{0o022, 0o600, 0o600},
		{0, 0o700, 0o700},
		{0, 0o777, 0o700},
		{0o077, 0o755, 0o700},
	} {
		// Create the directory before changing the umask: under 0277
		// MkdirTemp would create it without the write bit the bind needs.
		p := socketPath(t, "s.sock")
		old := syscall.Umask(tc.inherited)
		ln, err := ListenUnix(p, tc.mode)
		restored := syscall.Umask(old)
		if err != nil {
			t.Fatalf("ListenUnix(%#o) under %#o: %v", tc.mode, tc.inherited, err)
		}
		got := perm(t, p)
		_ = ln.Close()
		if got != tc.want {
			t.Errorf("mode %#o under umask %#o: socket %#o, want %#o", tc.mode, tc.inherited, got, tc.want)
		}
		if restored != tc.inherited {
			t.Errorf("umask after ListenUnix = %#o, want %#o", restored, tc.inherited)
		}
	}
}

func TestBindUmask(t *testing.T) {
	for mode, want := range map[os.FileMode]int{0o700: 0o077, 0o600: 0o177, 0o777: 0o077, 0: 0o777} {
		if got := BindUmask(mode); got != want {
			t.Errorf("BindUmask(%#o) = %#o, want %#o", mode, got, want)
		}
	}
}

// A second call must not enter the critical section while the first holds the
// temporary umask. The first call's hook starts a second ListenUnix and waits;
// without the mutex the second enters its hook while the first is still inside
// it, and the nesting depth reaches 2. With the mutex the second blocks until
// the first has returned, so the depth never exceeds 1.
func TestListenUnixSerializesConcurrentCalls(t *testing.T) {
	withUmask(t, 0o022)
	first, second := socketPath(t, "a.sock"), socketPath(t, "b.sock")

	var depth, maxDepth atomic.Int32
	var once sync.Once
	var wg sync.WaitGroup
	errs := make(chan error, 1)

	orig := afterUmask
	t.Cleanup(func() { afterUmask = orig })
	afterUmask = func() {
		d := depth.Add(1)
		for {
			m := maxDepth.Load()
			if d <= m || maxDepth.CompareAndSwap(m, d) {
				break
			}
		}
		once.Do(func() {
			wg.Go(func() {
				ln, err := ListenUnix(second, 0o700)
				if err != nil {
					errs <- err
					return
				}
				_ = ln.Close()
			})
			// Give the second call ample time to reach its hook. With the
			// mutex it cannot, whatever the timing, so this never makes a
			// correct implementation fail.
			time.Sleep(50 * time.Millisecond)
		})
		depth.Add(-1)
	}

	listen(t, first)
	wg.Wait()
	close(errs)
	if err := <-errs; err != nil {
		t.Fatalf("second ListenUnix: %v", err)
	}

	if got := maxDepth.Load(); got != 1 {
		t.Fatalf("critical section nesting depth = %d, want 1: concurrent calls were not serialized", got)
	}
	if got := currentUmask(); got != 0o022 {
		t.Fatalf("umask after concurrent ListenUnix = %#o, want 0022", got)
	}
}
