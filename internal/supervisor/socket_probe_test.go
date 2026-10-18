package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

// SEC-024 / ADR-027: the socket-directory writability probe runs before the
// privilege drop, so it must never open, follow or truncate a pre-existing
// path. The old probe opened a fixed name with os.Create (O_TRUNC, no O_EXCL,
// no O_NOFOLLOW), so a symlink planted at that name was followed and its target
// truncated, and the link was then removed, hiding the evidence.

func TestValidateSocketPermissionsDoesNotFollowSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "victim")
	const content = "must survive the probe\n"
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".kahi_perm_check")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := ValidateSocketPermissions(filepath.Join(dir, "kahi.sock")); err != nil {
		t.Fatalf("ValidateSocketPermissions: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("probe truncated the symlink target: content = %q, want %q", got, content)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("probe removed the planted symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("planted path is no longer a symlink: mode %v", info.Mode())
	}
}

func TestValidateSocketPermissionsLeavesNoProbe(t *testing.T) {
	dir := t.TempDir()
	if err := ValidateSocketPermissions(filepath.Join(dir, "kahi.sock")); err != nil {
		t.Fatalf("ValidateSocketPermissions: %v", err)
	}
	left, err := filepath.Glob(filepath.Join(dir, ".kahi_perm_check*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("probe file left behind: %v", left)
	}
}

// socketDir is pure, so the directory mapping is tested without writing into
// "/" (which needs root on Linux and is read-only on macOS). The first two rows
// are the cases the old hand-written extraction got wrong.
func TestSocketDir(t *testing.T) {
	tests := []struct{ path, want string }{
		{"/kahi.sock", "/"},                // old code: "." (probed the working directory)
		{"kahi.sock", "."},                 // old code: "kahi.sock" (statted the socket itself)
		{"/var/run/kahi.sock", "/var/run"}, // unchanged
		{"run/kahi.sock", "run"},           // unchanged
	}
	for _, tt := range tests {
		if got := socketDir(tt.path); got != tt.want {
			t.Errorf("socketDir(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// A bare relative socket name has no directory component. The old hand-written
// extraction kept the whole name as the "directory" and failed to stat it.
func TestValidateSocketPermissionsBareName(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := ValidateSocketPermissions("kahi.sock"); err != nil {
		t.Fatalf("ValidateSocketPermissions(%q): %v", "kahi.sock", err)
	}
}
