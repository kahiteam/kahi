package supervisor

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWritePIDFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kahi.pid")

	if err := WritePIDFile(path); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		t.Fatalf("pid file content %q is not a number", pidStr)
	}
	if pid != os.Getpid() {
		t.Fatalf("pid = %d, want %d", pid, os.Getpid())
	}
}

func TestWritePIDFileEmpty(t *testing.T) {
	// Empty path should be a no-op.
	if err := WritePIDFile(""); err != nil {
		t.Fatal(err)
	}
}

func TestRemovePIDFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kahi.pid")

	_ = WritePIDFile(path)
	RemovePIDFile(path)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("pid file should be removed")
	}
}

func TestRemovePIDFileEmpty(t *testing.T) {
	// Should not panic.
	RemovePIDFile("")
}

func TestValidateSocketPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kahi.sock")

	err := ValidateSocketPermissions(path)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateSocketPermissionsNonexistentDir(t *testing.T) {
	err := ValidateSocketPermissions("/nonexistent/dir/kahi.sock")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestValidateUnprivileged(t *testing.T) {
	// Just verify it doesn't error for non-root.
	original := getuid
	getuid = func() int { return 1000 }
	defer func() { getuid = original }()

	if err := ValidateUnprivileged(testLogger()); err != nil {
		t.Fatal(err)
	}
}

func TestResolveUser(t *testing.T) {
	uid, gid, err := resolveUser("1000:1000")
	if err != nil {
		t.Fatal(err)
	}
	if uid != 1000 || gid != 1000 {
		t.Fatalf("uid=%d, gid=%d, want 1000:1000", uid, gid)
	}
}

func TestResolveUserNoGroup(t *testing.T) {
	uid, gid, err := resolveUser("1000")
	if err != nil {
		t.Fatal(err)
	}
	if uid != 1000 || gid != 1000 {
		t.Fatalf("uid=%d, gid=%d, want 1000:1000", uid, gid)
	}
}

func TestResolveUserInvalid(t *testing.T) {
	_, _, err := resolveUser("notanumber")
	if err == nil {
		t.Fatal("expected error for invalid uid")
	}
}

func TestResolveUserInvalidGid(t *testing.T) {
	_, _, err := resolveUser("1000:abc")
	if err == nil {
		t.Fatal("expected error for invalid gid")
	}
}

func TestValidateUnprivilegedAsRoot(t *testing.T) {
	original := getuid
	getuid = func() int { return 0 }
	defer func() { getuid = original }()

	if err := ValidateUnprivileged(testLogger()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSocketPermissionsNotDirectory(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file where the parent "directory" should be.
	fakedir := filepath.Join(dir, "notadir")
	if err := os.WriteFile(fakedir, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ValidateSocketPermissions(fakedir + "/kahi.sock")
	if err == nil {
		t.Fatal("expected error for non-directory parent")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSocketPermissionsNoWritePermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode 0555, so the probe cannot be denied")
	}
	dir := t.TempDir()
	restricted := filepath.Join(dir, "noperm")
	if err := os.Mkdir(restricted, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(restricted, 0755) })

	err := ValidateSocketPermissions(restricted + "/kahi.sock")
	if err == nil {
		t.Fatal("expected permission denied error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDropPrivilegesEmptyUser(t *testing.T) {
	if err := DropPrivileges("", testLogger()); err != nil {
		t.Fatal(err)
	}
}

func TestDropPrivilegesInvalidUser(t *testing.T) {
	err := DropPrivileges("notanumber", testLogger())
	if err == nil {
		t.Fatal("expected error for invalid user")
	}
}

func TestSplitUserGroupVariants(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"1000", []string{"1000"}},
		{"1000:1000", []string{"1000", "1000"}},
		{":1000", []string{"", "1000"}},
	}
	for _, tt := range tests {
		got := splitUserGroup(tt.input)
		if len(got) != len(tt.want) {
			t.Fatalf("splitUserGroup(%q) = %v, want %v", tt.input, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("splitUserGroup(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

// The per-user socket directory is created owner-only, and an existing one
// is accepted only if it is a real directory of this user with mode 0700
// (FUNC-044).
func TestEnsurePrivateDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "kahi-x")
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("created with %v, want 0700", fi.Mode().Perm())
	}
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatalf("existing 0700 dir: %v", err)
	}

	open := filepath.Join(base, "open")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{open, link, file} {
		if err := ensurePrivateDir(bad); err == nil || !strings.Contains(err.Error(), "must be a directory owned by uid") {
			t.Errorf("%s: err %v, want it refused", filepath.Base(bad), err)
		}
	}
}

// A PID file naming a live process refuses the start; a missing, stale or
// unparseable one does not (FUNC-064, #143).
func TestCheckPIDFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kahi.pid")
	if err := CheckPIDFile(path, discardLogger()); err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if err := CheckPIDFile("", discardLogger()); err != nil {
		t.Fatalf("no PID file: %v", err)
	}

	parent := os.Getppid()
	if err := os.WriteFile(path, []byte(strconv.Itoa(parent)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckPIDFile(path, discardLogger()); err == nil || !strings.Contains(err.Error(), "another instance running (pid "+strconv.Itoa(parent)+")") {
		t.Fatalf("live pid: %v", err)
	}

	for _, content := range []string{"999999999\n", "garbage\n", strconv.Itoa(os.Getpid()) + "\n"} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := CheckPIDFile(path, discardLogger()); err != nil {
			t.Errorf("PID file %q: %v, want it treated as stale", content, err)
		}
	}
}
