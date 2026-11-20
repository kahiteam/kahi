package process

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/logging"
	"github.com/kahiteam/kahi/internal/userspec"
	"golang.org/x/sys/unix"
)

// ProbePrograms returns, for kahi config check (FUNC-087), the problems that
// would make a program's spawn fail or its log go missing. None stops the
// daemon (the program goes BACKOFF, then FATAL), so they are warnings. Each
// numprocs instance is checked with its own expanded values; a message is
// returned once however many instances produce it. Nothing is spawned.
// relOK is false when the [supervisor] directory chdir failed: relative
// paths would resolve elsewhere than in the daemon, so they are skipped.
func ProbePrograms(cfg *config.Config, relOK bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, u := range probeUnits(cfg) {
		add := func(key string, err error) {
			msg := fmt.Sprintf("%s.%s.%s: %v", u.table, u.name, key, err)
			if !seen[msg] {
				seen[msg] = true
				out = append(out, msg)
			}
		}
		skip := func(path string) bool { return !relOK && path != "" && !filepath.IsAbs(path) }
		for _, inst := range ExpandNumprocs(u.name, u.cfg) {
			c := inst.Config
			dirOK := true
			if skip(c.Directory) {
				dirOK = false
			} else if err := probeProgramDir(c.Directory); err != nil {
				add("directory", err)
				dirOK = false
			}
			if err := probeCommand(inst, dirOK, relOK); err != nil {
				add("command", err)
			}
			if c.StdoutLogfile != "" && !skip(c.StdoutLogfile) {
				if err := logging.ProbeLogFile(c.StdoutLogfile); err != nil {
					add("stdout_logfile", err)
				}
			}
			if c.StderrLogfile != "" && !c.RedirectStderr && !skip(c.StderrLogfile) {
				if err := logging.ProbeLogFile(c.StderrLogfile); err != nil {
					add("stderr_logfile", err)
				}
			}
		}
	}
	return out
}

type probeUnit struct {
	table, name string
	cfg         config.ProgramConfig
}

// probeUnits lists the programs and event listener pools to check, programs
// first, each sorted by name. Pool members are spawned like programs
// (FUNC-055); their stdout carries the protocol, so only stderr has a log
// file to check.
func probeUnits(cfg *config.Config) []probeUnit {
	var units []probeUnit
	for name, p := range cfg.Programs {
		units = append(units, probeUnit{"programs", name, p})
	}
	for name, l := range cfg.EventListeners {
		p := l.ProgramConfig
		p.StdoutLogfile = ""
		units = append(units, probeUnit{"eventlisteners", name, p})
	}
	slices.SortFunc(units, func(a, b probeUnit) int {
		if a.table != b.table {
			return strings.Compare(b.table, a.table) // programs first
		}
		return strings.Compare(a.name, b.name)
	})
	return units
}

// probeProgramDir mirrors the directory check at the top of spawnLocked,
// plus the search permission chdir needs.
func probeProgramDir(dir string) error {
	if dir == "" {
		return nil
	}
	fi, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist) || (err == nil && !fi.IsDir()):
		return fmt.Errorf("directory does not exist: %s", dir)
	case err != nil:
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return fmt.Errorf("cannot access directory: %s: %v", dir, err)
	}
	if err := unix.Access(dir, unix.X_OK); err != nil {
		return fmt.Errorf("cannot access directory: %s: %v", dir, err)
	}
	return nil
}

// probeCommand resolves the program's command as the spawn does: a path with
// a slash is used as is, relative to the program's directory; a bare name is
// looked up in the supervisor's PATH (exec.Command), or in the program's
// environment PATH when umask or rlimits route the spawn through the
// trampoline (RunChildInit). A relative path is not checked when the
// directory itself is missing (that warning covers it).
func probeCommand(inst ProcessInstance, dirOK, relOK bool) error {
	c := inst.Config
	parts := strings.Fields(c.Command)
	if len(parts) == 0 {
		return nil
	}
	name := parts[0]
	if strings.Contains(name, "/") {
		path := name
		if !filepath.IsAbs(path) && c.Directory != "" {
			if !dirOK {
				return nil
			}
			path = filepath.Join(c.Directory, path)
		}
		if !relOK && !filepath.IsAbs(path) {
			return nil
		}
		return probeExecutable(name, path)
	}
	umask, err := ParseUmask(c.Umask)
	trampoline := len(ParseRLimits(c)) > 0 || (err == nil && umask >= 0)
	if !trampoline {
		_, err := exec.LookPath(name)
		return err
	}
	// The trampoline looks the name up with the child's environment.
	p := NewProcess(inst.Name, inst.Group, c, nil, nil, slog.New(slog.DiscardHandler))
	if c.User != "" {
		if cred, rerr := resolveUser(c.User); rerr == nil {
			return lookPathIn(name, p.buildEnvFor(cred, true), c.Directory, dirOK, relOK)
		}
	}
	return lookPathIn(name, p.buildEnvFor(userspec.Credential{}, false), c.Directory, dirOK, relOK)
}

// probeExecutable reports why exec would fail for path (display is the
// name as configured).
func probeExecutable(display, path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return fmt.Errorf("%s: %v", display, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("%s: %v", display, unix.EISDIR)
	}
	if err := unix.Access(path, unix.X_OK); err != nil {
		return fmt.Errorf("%s: %v", display, err)
	}
	return nil
}

// lookPathIn mirrors how the trampoline (RunChildInit) resolves a bare name
// with exec.LookPath and the PATH in env, in the program's directory (dir,
// or the daemon's when empty): the first hit wins; a hit through an
// absolute PATH entry runs, one through a relative entry is refused
// (exec.ErrDot), as a direct spawn refuses it. Relative entries are not
// judged when the directory is missing (its warning covers it) or cannot
// be resolved here.
func lookPathIn(name string, env []string, dir string, dirOK, relOK bool) error {
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	base := dir
	if base == "" {
		base = "."
	}
	judgeRel := dirOK && (relOK || filepath.IsAbs(base))
	for _, d := range filepath.SplitList(path) {
		if filepath.IsAbs(d) {
			if executable(filepath.Join(d, name)) {
				return nil
			}
			continue
		}
		if !judgeRel {
			return nil
		}
		if d == "" {
			d = "."
		}
		if executable(filepath.Join(base, d, name)) {
			return fmt.Errorf("exec: %q: cannot run executable found relative to current directory", name)
		}
	}
	return fmt.Errorf("exec: %q: executable file not found in $PATH", name)
}

func executable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && unix.Access(path, unix.X_OK) == nil
}
