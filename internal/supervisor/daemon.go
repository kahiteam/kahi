package supervisor

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
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
	lchown    = os.Lchown
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

// RemoveRuntimeFiles removes the PID file at shutdown and checks that the
// socket, which closing its listener unlinks, is gone too. A daemon that
// dropped privileges may lack write access to their directories: what is
// left gets one warning, and the next start as root replaces it (FUNC-045).
func RemoveRuntimeFiles(pidPath, socketPath string, logger *slog.Logger) {
	var left []string
	if pidPath != "" {
		if err := os.Remove(pidPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			left = append(left, pidPath)
		}
	}
	if socketPath != "" {
		if _, err := os.Lstat(socketPath); err == nil {
			left = append(left, socketPath)
		}
	}
	if len(left) > 0 {
		logger.Warn("cannot remove at shutdown; the next start replaces them", "paths", left)
	}
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

// ValidateSocketPermissions checks that the socket directory is writable,
// creating it when missing (see PrepareSocketDir).
func ValidateSocketPermissions(socketPath string) error {
	_, err := PrepareSocketDir(socketPath)
	return err
}

// PrepareSocketDir makes sure the socket's directory exists and is writable.
// A missing directory whose parent exists is created, one level only, mode
// 0700; created reports that, so the directory is handed over with the
// socket on a privilege drop (FUNC-044, ADR-045).
func PrepareSocketDir(socketPath string) (created bool, err error) {
	dir := socketDir(socketPath)

	// The per-user default directory is created here, owner-only (FUNC-044).
	if dir == config.PrivateSocketDir() {
		if created, err = ensurePrivateDir(dir); err != nil {
			return false, err
		}
	} else if err := os.Mkdir(dir, 0o700); err == nil {
		created = true
	} else if !errors.Is(err, fs.ErrExist) {
		// Mkdir fails with ENOENT when the parent is missing too.
		if _, serr := os.Stat(dir); serr != nil {
			return false, fmt.Errorf("socket directory does not exist: %s: %w", dir, serr)
		}
	}

	info, err := os.Stat(dir)
	if err != nil {
		return false, fmt.Errorf("socket directory does not exist: %s: %w", dir, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("socket path parent is not a directory: %s", dir)
	}

	// SEC-024 / ADR-027: check write permission by creating an exclusive,
	// randomly named temp file. This runs before the privilege drop, so it must
	// never open an existing path: a fixed name opened with os.Create (O_TRUNC,
	// no O_EXCL) follows a symlink planted at that name and truncates its target
	// as root. os.CreateTemp uses O_CREATE|O_EXCL, which fails on any existing
	// path including a symlink, under a name that cannot be predicted.
	f, err := os.CreateTemp(dir, ".kahi_perm_check*")
	if err != nil {
		return false, fmt.Errorf("permission denied: cannot create socket in %s: %w", dir, err)
	}
	// A failed close or remove is not fatal: the directory is already proven
	// writable and the leftover file has a random name nothing else opens.
	// Removing by name is safe regardless, since unlink(2) never follows links.
	_ = f.Close()
	_ = os.Remove(f.Name())

	return created, nil
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

// idToInt converts a uid or gid for the setuid/setgid/setgroups calls, which
// take int. On 32-bit targets an id above MaxInt32 would wrap negative, so it
// is refused on every target alike.
func idToInt(kind string, id uint32) (int, error) {
	if id > math.MaxInt32 {
		return 0, fmt.Errorf("privilege drop failed: %s %d out of range", kind, id)
	}
	return int(id), nil
}

// DropPlan is the privilege drop the daemon will make (FUNC-045).
type DropPlan struct {
	Drop     bool                // drop privileges after binding
	Cred     userspec.Credential // the resolved target user
	Override bool                // kahi daemon -u overrides [supervisor] user
	Warning  string              // [supervisor] user ignored as non-root
}

// PlanDrop resolves the user to drop to, before anything is bound. kahi
// daemon -u wins over [supervisor] user and fails closed as non-root; the
// config key only warns as non-root, because one config file is often
// shared between root and non-root runs. An unknown user is an error.
func PlanDrop(flagUser, cfgUser string) (DropPlan, error) {
	if flagUser != "" {
		cred, err := CheckPrivilegeDrop(flagUser)
		if err != nil {
			return DropPlan{}, err
		}
		return DropPlan{Drop: true, Cred: cred, Override: cfgUser != ""}, nil
	}
	if cfgUser == "" {
		return DropPlan{}, nil
	}
	if uid := getuid(); uid != 0 {
		return DropPlan{Warning: fmt.Sprintf("supervisor.user ignored: dropping privileges needs root (running as uid %d)", uid)}, nil
	}
	cred, err := resolveDropUser(cfgUser)
	if err != nil {
		return DropPlan{}, err
	}
	return DropPlan{Drop: true, Cred: cred}, nil
}

// DropPrivileges switches the process to the resolved user: its uid, primary
// gid and supplementary groups (SEC-010). First it hands the files the
// daemon created as root (socket, socket directory when created, PID file,
// daemon log) to that user with lchown, which never follows a symlink, so
// kahi ctl works as the user and shutdown can remove them (ADR-045). Empty
// paths are skipped.
func DropPrivileges(cred userspec.Credential, paths []string, logger *slog.Logger) error {
	uid, err := idToInt("uid", cred.Uid)
	if err != nil {
		return err
	}
	gid, err := idToInt("gid", cred.Gid)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := lchown(p, uid, gid); err != nil {
			var pe *fs.PathError
			if errors.As(err, &pe) {
				err = pe.Err
			}
			return fmt.Errorf("privilege drop failed: chown %s: %v", p, err)
		}
	}

	ids := cred.Groups
	if len(ids) == 0 {
		ids = []uint32{cred.Gid}
	}
	groups := make([]int, len(ids))
	for i, g := range ids {
		v, err := idToInt("gid", g)
		if err != nil {
			return err
		}
		groups[i] = v
	}
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
func ensurePrivateDir(dir string) (created bool, err error) {
	if err := os.Mkdir(dir, 0o700); err == nil {
		created = true
	} else if !os.IsExist(err) {
		return false, fmt.Errorf("cannot create socket directory %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return false, fmt.Errorf("socket directory %s: %w", dir, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(st.Uid) != os.Geteuid() || info.Mode().Perm()&0o077 != 0 {
		return false, fmt.Errorf("socket directory %s must be a directory owned by uid %d with mode 0700; remove it or set [server.unix] file", dir, os.Geteuid())
	}
	return created, nil
}
