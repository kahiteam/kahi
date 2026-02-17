package supervisor

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"syscall"
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

// ValidateSocketPermissions checks that the socket directory is writable.
func ValidateSocketPermissions(socketPath string) error {
	dir := socketPath
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '/' {
			dir = dir[:i]
			break
		}
	}
	if dir == "" {
		dir = "."
	}

	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("socket directory does not exist: %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("socket path parent is not a directory: %s", dir)
	}

	// Check write permission by trying to create a temp file.
	tmpPath := dir + "/.kahi_perm_check"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("permission denied: cannot create socket in %s: %w", dir, err)
	}
	f.Close()
	os.Remove(tmpPath)

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
	devNull.Close()

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

	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("setgid(%d) failed: %w", gid, err)
	}
	if err := syscall.Setuid(uid); err != nil {
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
