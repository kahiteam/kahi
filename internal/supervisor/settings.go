package supervisor

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"syscall"

	"github.com/kahiteam/kahi/internal/config"
	"golang.org/x/sys/unix"
)

// ChangeDirectory applies [supervisor] directory at startup, before the
// daemon opens its log, binds a socket or starts a process (FUNC-067,
// ADR-047). An empty dir keeps the inherited working directory.
func ChangeDirectory(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.Chdir(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("directory does not exist: %s", dir)
		}
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return fmt.Errorf("cannot access directory: %s: %v", dir, err)
	}
	return nil
}

// SetUmask applies [supervisor] umask at startup, before the daemon creates
// any file (FUNC-047, ADR-047). An empty value keeps the inherited umask; the
// value is validated at config load.
func SetUmask(s string) error {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil || v > 0o777 {
		return fmt.Errorf("invalid umask: %s", s)
	}
	syscall.Umask(int(v))
	return nil
}

// rlimitOps is the getrlimit/setrlimit seam, replaced in tests.
type rlimitOps struct {
	get  func(resource int, lim *unix.Rlimit) error
	set  func(resource int, lim *unix.Rlimit) error
	goos string
	// maxFiles returns kern.maxfilesperproc on macOS.
	maxFiles func() (uint64, bool)
}

var defaultRlimitOps = rlimitOps{
	get:      unix.Getrlimit,
	set:      unix.Setrlimit,
	goos:     runtime.GOOS,
	maxFiles: maxFilesPerProc,
}

// RaiseLimits raises the NOFILE and NPROC soft limits to minfds and
// minprocs when they are lower (FUNC-068, ADR-047). The explicit Setrlimit
// also makes children inherit the raised limit (Go otherwise restores the
// original NOFILE soft limit in children). A value written in the config
// that cannot be met is an error; a default that cannot be met raises the
// soft limit as far as the hard limit allows and warns.
func RaiseLimits(s config.SupervisorConfig, logger *slog.Logger) error {
	return raiseLimits(defaultRlimitOps, s, logger)
}

func raiseLimits(ops rlimitOps, s config.SupervisorConfig, logger *slog.Logger) error {
	if err := setNofile(ops, uint64(max(s.Minfds, 0)), s.MinfdsSet, logger); err != nil {
		return err
	}
	return raiseLimit(ops, unix.RLIMIT_NPROC, "NPROC", "minprocs", 200, uint64(max(s.Minprocs, 0)), s.MinprocsSet, logger)
}

// setNofile applies minfds (decided 2026-11-15). Every Go program raises its
// own NOFILE soft limit to the hard limit at startup and os/exec gives
// children the original soft limit back unless the program calls Setrlimit;
// the original cannot be read. So an explicit minfds sets the soft limit to
// exactly minfds (raising the hard limit when it is lower and the daemon may),
// and programs inherit it. Without minfds the limits are left alone, so
// programs keep their original soft limit; a hard limit below the default
// only warns.
func setNofile(ops rlimitOps, want uint64, explicit bool, logger *slog.Logger) error {
	if want == 0 {
		return nil
	}
	var cur unix.Rlimit
	if err := ops.get(unix.RLIMIT_NOFILE, &cur); err != nil {
		return fmt.Errorf("cannot read NOFILE limit: %w", err)
	}
	current := fmt.Sprintf("(current soft=%d, hard=%d)", cur.Cur, cur.Max)
	if !explicit {
		if cur.Max < want {
			logger.Warn(fmt.Sprintf("NOFILE is %d, below the default minfds %d; set supervisor.minfds = %d or lower to silence this", cur.Max, want, cur.Max))
		}
		return nil
	}
	// macOS caps NOFILE at kern.maxfilesperproc even when the hard limit is
	// unlimited, and refuses more with EINVAL.
	if ops.goos == "darwin" && ops.maxFiles != nil {
		if m, ok := ops.maxFiles(); ok && want > m {
			return fmt.Errorf("cannot set NOFILE to %d: exceeds kern.maxfilesperproc (%d) %s", want, m, current)
		}
	}
	lim := unix.Rlimit{Cur: want, Max: cur.Max}
	if want > cur.Max {
		lim.Max = want
	}
	if err := ops.set(unix.RLIMIT_NOFILE, &lim); err != nil {
		if want > cur.Max {
			return fmt.Errorf("cannot set NOFILE to %d: hard limit is %d %s", want, cur.Max, current)
		}
		return fmt.Errorf("cannot set NOFILE to %d: %v %s", want, err, current)
	}
	return nil
}

func raiseLimit(ops rlimitOps, resource int, name, key string, def, want uint64, explicit bool, logger *slog.Logger) error {
	var cur unix.Rlimit
	if err := ops.get(resource, &cur); err != nil {
		return fmt.Errorf("cannot read %s limit: %w", name, err)
	}
	if want == 0 || cur.Cur >= want {
		return nil
	}
	current := fmt.Sprintf("(current soft=%d, hard=%d)", cur.Cur, cur.Max)

	// macOS caps NOFILE at kern.maxfilesperproc even when the hard limit is
	// unlimited, and refuses more with EINVAL.
	if resource == unix.RLIMIT_NOFILE && ops.goos == "darwin" && ops.maxFiles != nil {
		if m, ok := ops.maxFiles(); ok && want > m {
			msg := fmt.Sprintf("cannot set NOFILE to %d: exceeds kern.maxfilesperproc (%d) %s", want, m, current)
			if explicit {
				return errors.New(msg)
			}
			logger.Warn(msg)
			return nil
		}
	}

	if want <= cur.Max {
		lim := unix.Rlimit{Cur: want, Max: cur.Max}
		if err := ops.set(resource, &lim); err != nil {
			return limitFailure(ops, name, want, err, current, explicit, logger)
		}
		return nil
	}

	// Above the hard limit: only a daemon with CAP_SYS_RESOURCE (root
	// outside a restricted container) may raise the hard limit too.
	if err := ops.set(resource, &unix.Rlimit{Cur: want, Max: want}); err == nil {
		return nil
	}
	if explicit {
		if resource == unix.RLIMIT_NPROC && ops.goos == "darwin" {
			logger.Warn(fmt.Sprintf("cannot set NPROC to %d: hard limit is %d; continuing (macOS)", want, cur.Max))
			return nil
		}
		return fmt.Errorf("cannot set %s to %d: hard limit is %d %s", name, want, cur.Max, current)
	}
	if cur.Cur < cur.Max {
		_ = ops.set(resource, &unix.Rlimit{Cur: cur.Max, Max: cur.Max})
	}
	logger.Warn(fmt.Sprintf("%s is %d, below the default %s %d; set supervisor.%s = %d or lower to silence this", name, cur.Max, key, def, key, cur.Max))
	return nil
}

func limitFailure(ops rlimitOps, name string, want uint64, err error, current string, explicit bool, logger *slog.Logger) error {
	if name == "NPROC" && ops.goos == "darwin" {
		logger.Warn(fmt.Sprintf("cannot set NPROC to %d: %v; continuing (macOS)", want, err))
		return nil
	}
	msg := fmt.Sprintf("cannot set %s to %d: %v %s", name, want, err, current)
	if explicit {
		return errors.New(msg)
	}
	logger.Warn(msg)
	return nil
}
