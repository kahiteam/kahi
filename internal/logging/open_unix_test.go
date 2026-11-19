//go:build unix

package logging

import (
	"os"
	"path/filepath"
	"testing"
)

// SEC-019 / ADR-022: openLogFile walks every path component with O_NOFOLLOW so
// a symlink swapped into a parent directory cannot redirect log writes. These
// tests exercise the parent-symlink case in addition to the SEC-012
// final-component guard.

// foreignLinks makes the links the test creates count as planted by another
// user: only root's links are trusted (unit tests cannot chown a link).
func foreignLinks(t *testing.T) {
	t.Helper()
	old := trustedLinkOwner
	trustedLinkOwner = func(uid uint32) bool { return uid == 0 }
	t.Cleanup(func() { trustedLinkOwner = old })
}

// TestLogOpenRejectsSymlinkedParent verifies that a symlinked parent component
// causes the open to fail rather than being followed to an unintended dir.
func TestLogOpenRejectsSymlinkedParent(t *testing.T) {
	foreignLinks(t)
	base := t.TempDir()
	evil := filepath.Join(base, "evil")
	if err := os.Mkdir(evil, 0755); err != nil {
		t.Fatal(err)
	}
	// "link" is a parent component of the log path but is a symlink.
	link := filepath.Join(base, "link")
	if err := os.Symlink(evil, link); err != nil {
		t.Fatal(err)
	}
	logpath := filepath.Join(link, "app.log")

	if _, err := openLogFile(logpath, 0644); err == nil {
		t.Fatal("expected error opening through a symlinked parent component")
	}
}

// TestLogOpenWriteNotRedirectedThroughSwappedParent verifies that when a parent
// component is a symlink the open fails and no file is created in the target of
// the swapped parent.
func TestLogOpenWriteNotRedirectedThroughSwappedParent(t *testing.T) {
	foreignLinks(t)
	base := t.TempDir()
	evil := filepath.Join(base, "evil")
	if err := os.Mkdir(evil, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "logs")
	if err := os.Symlink(evil, link); err != nil {
		t.Fatal(err)
	}
	logpath := filepath.Join(link, "app.log")

	f, err := openLogFile(logpath, 0644)
	if err == nil {
		_ = f.Close()
		t.Fatal("expected open to fail on symlinked parent")
	}
	if _, err := os.Stat(filepath.Join(evil, "app.log")); err == nil {
		t.Fatal("write was redirected through the swapped parent directory")
	}
}

// TestLogOpenRejectsSymlinkedAncestor verifies the guard applies to any
// ancestor component, not only the immediate parent.
func TestLogOpenRejectsSymlinkedAncestor(t *testing.T) {
	foreignLinks(t)
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// "link" is an ancestor two levels above the file.
	logpath := filepath.Join(link, "sub", "app.log")

	if _, err := openLogFile(logpath, 0644); err == nil {
		t.Fatal("expected error opening through a symlinked ancestor component")
	}
}

// TestLogOpenRejectsSymlinkedFinalComponent verifies the SEC-012 guard is
// preserved: a symlink at the final component is still rejected.
func TestLogOpenRejectsSymlinkedFinalComponent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	if err := os.WriteFile(target, nil, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, err := openLogFile(link, 0644); err == nil {
		t.Fatal("expected error opening a symlinked final component (O_NOFOLLOW)")
	}
}

// TestLogOpenRejectsInTreeSymlinkedFinalComponent verifies O_NOFOLLOW rejects a
// final-component symlink even when its target stays within the filesystem
// root (a relative symlink), which os.Root alone would follow. This locks the
// SEC-012 semantics that any symlinked log path is refused.
func TestLogOpenRejectsInTreeSymlinkedFinalComponent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "target.log"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.log")
	if err := os.Symlink("target.log", link); err != nil {
		t.Fatal(err)
	}

	if _, err := openLogFile(link, 0644); err == nil {
		t.Fatal("expected error opening an in-tree symlinked final component")
	}
}

// TestLogOpenNormalNestedPath verifies a normal path with no symlinked
// components opens and is writable, unchanged from prior behavior.
func TestLogOpenNormalNestedPath(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, "a", "b", "c")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatal(err)
	}
	logpath := filepath.Join(deep, "app.log")

	f, err := openLogFile(logpath, 0644)
	if err != nil {
		t.Fatalf("normal nested path should open: %v", err)
	}
	if _, err := f.WriteString("hello\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(logpath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello\n" {
		t.Fatalf("unexpected file contents: %q", string(got))
	}
}

// TestLogOpenAppends verifies O_APPEND semantics survive the hardened open.
func TestLogOpenAppends(t *testing.T) {
	dir := t.TempDir()
	logpath := filepath.Join(dir, "app.log")

	for _, line := range []string{"one\n", "two\n"} {
		f, err := openLogFile(logpath, 0644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	got, err := os.ReadFile(logpath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one\ntwo\n" {
		t.Fatalf("expected appended contents, got %q", string(got))
	}
}

// A relative symlink planted in a log directory used to be followed out of
// it (os.Root rooted at "/" treats every target as in-tree); a link another
// user owns is refused, relative or not.
func TestLogOpenRejectsForeignRelativeLink(t *testing.T) {
	foreignLinks(t)
	base := t.TempDir()
	evil := filepath.Join(base, "etc", "cron.d")
	if err := os.MkdirAll(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(base, "var", "log")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../etc/cron.d", filepath.Join(logs, "app")); err != nil {
		t.Fatal(err)
	}
	logpath := filepath.Join(logs, "app", "app.log")
	if f, err := openLogFile(logpath, 0o644); err == nil {
		_ = f.Close()
		t.Fatal("followed a foreign relative symlink")
	}
	if _, err := os.Stat(filepath.Join(evil, "app.log")); err == nil {
		t.Fatal("the log was created through the planted link")
	}
	if err := probeLogFile(logpath); err == nil {
		t.Error("probe accepts a foreign relative symlink")
	}
}

// Links root or the daemon's user own are followed, relative and absolute
// (the test user is the daemon's user here).
func TestLogOpenFollowsTrustedLinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(base, "rel")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(base, "abs")); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{"rel", "abs"} {
		f, err := openLogFile(filepath.Join(base, link, link+".log"), 0o644)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		_ = f.Close()
		if _, err := os.Stat(filepath.Join(real, link+".log")); err != nil {
			t.Errorf("%s: not created in the link target: %v", link, err)
		}
	}
}

// Rotation renames and removes relative to the verified directory: a log
// directory swapped for a foreign link fails the rotation instead of
// moving or deleting files in the link's target.
func TestRotationRefusesForeignParentLink(t *testing.T) {
	foreignLinks(t)
	base := t.TempDir()
	evil := filepath.Join(base, "evil")
	if err := os.Mkdir(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"app.log", "app.log.1"} {
		if err := os.WriteFile(filepath.Join(evil, n), []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("evil", filepath.Join(base, "logs")); err != nil {
		t.Fatal(err)
	}
	if err := rotateFile(filepath.Join(base, "logs", "app.log"), 1); err == nil {
		t.Fatal("rotated through a foreign link")
	}
	for _, n := range []string{"app.log", "app.log.1"} {
		if b, err := os.ReadFile(filepath.Join(evil, n)); err != nil || string(b) != "keep" {
			t.Errorf("%s changed: %q %v", n, b, err)
		}
	}
}
