package supervisor

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
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

// Daemonize performs a double-fork to become a background daemon.
// Returns true in the parent (which should exit), false in the daemon child.
func Daemonize(logger *slog.Logger) (bool, error) {
	// First fork.
	pid, errno := sysFork()
	if errno != 0 {
		return false, fmt.Errorf("first fork failed: %v", errno)
	}
	if pid > 0 {
		// Parent process -- exit.
		return true, nil
	}

	// Create new session.
	if _, err := syscall.Setsid(); err != nil {
		return false, fmt.Errorf("setsid failed: %w", err)
	}

	// Second fork.
	pid, errno = sysFork()
	if errno != 0 {
		return false, fmt.Errorf("second fork failed: %v", errno)
	}
	if pid > 0 {
		// First child -- exit.
		os.Exit(0)
	}

	// Redirect stdio to /dev/null.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return false, fmt.Errorf("cannot open /dev/null: %w", err)
	}
	_ = sysDup2(int(devNull.Fd()), int(os.Stdin.Fd()))
	_ = sysDup2(int(devNull.Fd()), int(os.Stdout.Fd()))
	_ = sysDup2(int(devNull.Fd()), int(os.Stderr.Fd()))
	_ = devNull.Close()

	logger.Info("daemonized", "pid", os.Getpid())
	return false, nil
}

// DropPrivileges switches the process to the given user/group.
func DropPrivileges(user string, logger *slog.Logger) error {
	if user == "" {
		return nil
	}

	uid, gid, err := resolveUser(user)
	if err != nil {
		return fmt.Errorf("cannot resolve user %q: %w", user, err)
	}

	// Reset supplementary groups before changing gid/uid so the dropped process
	// does not retain the launching user's group memberships (e.g. docker, wheel,
	// which can be root-equivalent). Order matters: setgroups, then setgid, then
	// setuid -- once uid is dropped the process can no longer call setgroups. Any
	// failure aborts rather than leaving privileges partially dropped.
	if err := setgroups([]int{gid}); err != nil {
		return fmt.Errorf("setgroups failed: %w", err)
	}
	if err := setgid(gid); err != nil {
		return fmt.Errorf("setgid(%d) failed: %w", gid, err)
	}
	if err := setuid(uid); err != nil {
		return fmt.Errorf("setuid(%d) failed: %w", uid, err)
	}

	logger.Info("dropped privileges", "uid", uid, "gid", gid)
	return nil
}

// resolveUser parses "user" or "user:group" into uid and gid.
func resolveUser(user string) (int, int, error) {
	parts := splitUserGroup(user)

	uid, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid uid: %s", parts[0])
	}

	gid := uid // default gid = uid
	if len(parts) > 1 {
		gid, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid gid: %s", parts[1])
		}
	}

	return uid, gid, nil
}

func splitUserGroup(s string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}
