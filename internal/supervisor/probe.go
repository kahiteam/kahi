package supervisor

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/userspec"
	"golang.org/x/sys/unix"
)

// Probes for kahi config check (FUNC-087, ADR-054). Each one answers whether
// the startup step beside it would fail, with the same message prefix and
// errno, and only reads: stat, lstat, access(2), getrlimit, sysctl, reading
// the PID file and kill(pid, 0). None creates, writes, binds or spawns.

// access is the access(2) seam, replaced in tests.
var access = unix.Access

// ProbeDirectory reports whether ChangeDirectory(dir) would fail.
func ProbeDirectory(dir string) error {
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("directory does not exist: %s", dir)
	}
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return fmt.Errorf("cannot access directory: %s: %v", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cannot access directory: %s: %v", dir, syscall.ENOTDIR)
	}
	if err := access(dir, unix.X_OK); err != nil {
		return fmt.Errorf("cannot access directory: %s: %v", dir, err)
	}
	return nil
}

// ProbeLimits reports whether RaiseLimits would fail, through the same code
// with a setrlimit that only judges: a soft limit up to the hard limit is
// allowed, a higher hard limit only as root. Whether root may raise the hard
// limit (CAP_SYS_RESOURCE) is known only by trying, so as root that case is
// logged as a note (Info) and assumed to work. Warnings go to logger as the
// daemon logs them.
func ProbeLimits(s config.SupervisorConfig, logger *slog.Logger) error {
	return probeLimits(defaultRlimitOps, s, logger)
}

func probeLimits(ops rlimitOps, s config.SupervisorConfig, logger *slog.Logger) error {
	ops.set = func(resource int, lim *unix.Rlimit) error {
		var cur unix.Rlimit
		if err := ops.get(resource, &cur); err != nil {
			return err
		}
		if lim.Max <= cur.Max {
			return nil
		}
		if geteuid() != 0 {
			return unix.EPERM
		}
		logger.Info(fmt.Sprintf("raising the %s hard limit to %d needs CAP_SYS_RESOURCE; not checked", rlimitName(resource), lim.Max))
		return nil
	}
	return raiseLimits(ops, s, logger)
}

func rlimitName(resource int) string {
	if resource == unix.RLIMIT_NOFILE {
		return "NOFILE"
	}
	return "NPROC"
}

// geteuid is the effective-uid seam, replaced in tests.
var geteuid = os.Geteuid

// ProbeSocketDir reports whether PrepareSocketDir(socketPath) would fail.
// A missing directory is fine when PrepareSocketDir would create it.
func ProbeSocketDir(socketPath string) error {
	dir := socketDir(socketPath)
	if dir == config.PrivateSocketDir() {
		return probePrivateDir(dir)
	}
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		// Mkdir creates one level: it needs an existing, writable parent.
		if perr := probeWritableDir(filepath.Dir(dir)); perr != nil {
			return fmt.Errorf("socket directory does not exist: %s: %w", dir, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("socket directory does not exist: %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("socket path parent is not a directory: %s", dir)
	}
	if err := access(dir, unix.W_OK|unix.X_OK); err != nil {
		return fmt.Errorf("permission denied: cannot create socket in %s: %w", dir, &fs.PathError{Op: "open", Path: dir, Err: err})
	}
	return nil
}

// probePrivateDir mirrors ensurePrivateDir without creating the directory.
func probePrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if perr := probeWritableDir(filepath.Dir(dir)); perr != nil {
			return fmt.Errorf("cannot create socket directory %s: %w", dir, &fs.PathError{Op: "mkdir", Path: dir, Err: perr})
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("socket directory %s: %w", dir, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(st.Uid) != os.Geteuid() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("socket directory %s must be a directory owned by uid %d with mode 0700; remove it or set [server.unix] file", dir, os.Geteuid())
	}
	// PrepareSocketDir then creates a file in it (os.CreateTemp).
	if err := access(dir, unix.W_OK|unix.X_OK); err != nil {
		return fmt.Errorf("permission denied: cannot create socket in %s: %w", dir, &fs.PathError{Op: "open", Path: dir, Err: err})
	}
	return nil
}

// probeWritableDir returns the errno that creating an entry in dir would
// fail with, or nil.
func probeWritableDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return pe.Err
		}
		return err
	}
	if !info.IsDir() {
		return syscall.ENOTDIR
	}
	return access(dir, unix.W_OK|unix.X_OK)
}

// ProbePIDFile reports what CheckPIDFile and WritePIDFile would do with
// path: live is the pid of a running daemon the file names (CheckPIDFile
// would refuse the start; the check only notes it), a stale file is logged
// as CheckPIDFile logs it, and err is what either would fail with.
func ProbePIDFile(path string, logger *slog.Logger) (live int, err error) {
	if path == "" {
		return 0, nil
	}
	content, exists, err := readPIDFile(path)
	switch {
	case err != nil:
		return 0, err
	case !exists:
	default:
		pid, perr := strconv.Atoi(content)
		if perr == nil && pid > 0 && pid != os.Getpid() {
			if kerr := syscall.Kill(pid, 0); kerr == nil || kerr == syscall.EPERM {
				live = pid
			}
		}
		if live == 0 {
			logger.Warn("replacing stale PID file", "path", path, "content", content)
		}
	}
	// WritePIDFile unlinks the old file and creates a new one: both need a
	// writable directory.
	if werr := probeWritableDir(filepath.Dir(path)); werr != nil {
		return live, fmt.Errorf("cannot write PID file: %s: %w", path, &fs.PathError{Op: "open", Path: path, Err: werr})
	}
	return live, nil
}

// DropCheck is what kahi config check learns about the privilege drop.
type DropCheck struct {
	Cred     userspec.Credential // the resolved user, when Drop
	Drop     bool                // a user to drop to is configured and resolves
	Override bool                // kahi daemon -u overrides [supervisor] user
	Note     string              // the part of the drop that was not checked
}

// CheckDrop resolves the user kahi daemon would drop to (-u, else
// [supervisor] user) with the resolver PlanDrop uses. Unlike PlanDrop it does
// not require root: as non-root it notes that the drop was not checked, so a
// non-root CI job can check a production config (FUNC-087, decided
// 2026-11-16). An unresolvable user is an error, as in PlanDrop.
func CheckDrop(flagUser, cfgUser string) (DropCheck, error) {
	name := flagUser
	if name == "" {
		name = cfgUser
	}
	if name == "" {
		return DropCheck{}, nil
	}
	cred, err := resolveDropUser(name)
	if err != nil {
		return DropCheck{}, err
	}
	dc := DropCheck{Drop: true, Cred: cred, Override: flagUser != "" && cfgUser != ""}
	if getuid() != 0 {
		dc.Note = "not root; the privilege drop was not checked"
	}
	return dc, nil
}
