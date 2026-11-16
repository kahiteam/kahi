package supervisor

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/userspec"
)

// Syscall seams for the privilege drop, overridable in tests so the call
// ordering and supplementary-group reset can be asserted without root.
var (
	setgroups = syscall.Setgroups
	setgid    = syscall.Setgid
	setuid    = syscall.Setuid
)

// WritePIDFile writes the current process PID to the given path.
func WritePIDFile(path string) error {
	if path == "" {
		return nil
	}
	pid := os.Getpid()
	data := []byte(strconv.Itoa(pid) + "\n")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("cannot write PID file: %s: %w", path, err)
	}
	return nil
}

// RemovePIDFile removes the PID file if it exists.
func RemovePIDFile(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// ValidateUnprivileged checks that the daemon is not running as root
// when it shouldn't be. Returns a descriptive error for permission issues.
func ValidateUnprivileged(logger *slog.Logger) error {
	uid := getuid()
	if uid == 0 {
		logger.Warn("running as root; consider using a non-root user")
	}
	return nil
}

// socketDir returns the directory a socket at socketPath is created in.
//
// SEC-024: this replaces a hand-written scan for the last "/", which mapped
// "/kahi.sock" to "." (probing the working directory instead of "/") and left a
// bare "kahi.sock" as its own "directory", so a valid relative path failed.
func socketDir(socketPath string) string {
	return filepath.Dir(socketPath)
}

// ValidateSocketPermissions checks that the socket directory is writable.
func ValidateSocketPermissions(socketPath string) error {
	dir := socketDir(socketPath)

	// The per-user default directory is created here, owner-only (FUNC-044).
	if dir == config.PrivateSocketDir() {
		if err := ensurePrivateDir(dir); err != nil {
			return err
		}
	}

	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("socket directory does not exist: %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("socket path parent is not a directory: %s", dir)
	}

	// SEC-024 / ADR-027: check write permission by creating an exclusive,
	// randomly named temp file. This runs before the privilege drop, so it must
	// never open an existing path: a fixed name opened with os.Create (O_TRUNC,
	// no O_EXCL) follows a symlink planted at that name and truncates its target
	// as root. os.CreateTemp uses O_CREATE|O_EXCL, which fails on any existing
	// path including a symlink, under a name that cannot be predicted.
	f, err := os.CreateTemp(dir, ".kahi_perm_check*")
	if err != nil {
		return fmt.Errorf("permission denied: cannot create socket in %s: %w", dir, err)
	}
	// A failed close or remove is not fatal: the directory is already proven
	// writable and the leftover file has a random name nothing else opens.
	// Removing by name is safe regardless, since unlink(2) never follows links.
	_ = f.Close()
	_ = os.Remove(f.Name())

	return nil
}

// CheckPIDFile refuses to start when path names a PID that is still alive,
// and warns about and leaves a stale file to be overwritten (FUNC-064, #143).
// It runs before the socket is bound, so a second daemon reports the PID
// file rather than a socket conflict.
func CheckPIDFile(path string, logger *slog.Logger) error {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read PID file: %s: %w", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err == nil && pid > 0 && pid != os.Getpid() {
		// EPERM means the process exists but belongs to another user.
		if err := syscall.Kill(pid, 0); err == nil || err == syscall.EPERM {
			return fmt.Errorf("another instance running (pid %d); PID file %s", pid, path)
		}
	}
	logger.Warn("replacing stale PID file", "path", path, "content", strings.TrimSpace(string(b)))
	return nil
}

// resolveDropUser resolves the user to drop to (ADR-046). Tests replace it
// so they never read the host's user database.
var resolveDropUser = userspec.Resolve

// CheckPrivilegeDrop resolves the kahi daemon -u user and reports, before
// anything is bound, whether the drop can be applied: the user must resolve
// and the daemon must run as root. Failing here, not after the socket is
// bound, gives a clear error and leaves nothing behind.
func CheckPrivilegeDrop(user string) (userspec.Credential, error) {
	cred, err := resolveDropUser(user)
	if err != nil {
		return userspec.Credential{}, err
	}
	if uid := getuid(); uid != 0 {
		return userspec.Credential{}, fmt.Errorf("kahi daemon -u %s: dropping privileges needs root (running as uid %d)", user, uid)
	}
	return cred, nil
}

// DropPrivileges switches the process to the resolved user: its uid, primary
// gid and supplementary groups (SEC-010).
func DropPrivileges(cred userspec.Credential, logger *slog.Logger) error {
	groups := make([]int, len(cred.Groups))
	for i, g := range cred.Groups {
		groups[i] = int(g)
	}
	if len(groups) == 0 {
		groups = []int{int(cred.Gid)}
	}
	uid, gid := int(cred.Uid), int(cred.Gid)

	// Replace the supplementary groups before changing gid/uid so the dropped
	// process keeps none of the launching user's memberships (e.g. docker,
	// wheel, which can be root-equivalent), only the target user's own.
	// Order matters: setgroups, then setgid, then setuid -- once uid is
	// dropped the process can no longer call setgroups. Any failure aborts
	// rather than leaving privileges partially dropped.
	if err := setgroups(groups); err != nil {
		return fmt.Errorf("privilege drop failed: setgroups failed: %w", err)
	}
	if err := setgid(gid); err != nil {
		return fmt.Errorf("privilege drop failed: setgid(%d) failed: %w", gid, err)
	}
	if err := setuid(uid); err != nil {
		return fmt.Errorf("privilege drop failed: setuid(%d) failed: %w", uid, err)
	}

	logger.Info("dropped privileges", "uid", uid, "gid", gid, "groups", groups)
	return nil
}

// ensurePrivateDir creates dir with mode 0700 if it is missing and refuses it
// unless it is a real directory owned by this user that group and others
// cannot reach: another user who created it first could otherwise replace or
// impersonate the socket in it.
func ensurePrivateDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("cannot create socket directory %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("socket directory %s: %w", dir, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(st.Uid) != os.Geteuid() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("socket directory %s must be a directory owned by uid %d with mode 0700; remove it or set [server.unix] file", dir, os.Geteuid())
	}
	return nil
}
