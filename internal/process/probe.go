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
func ProbePrograms(cfg *config.Config) []string {
	type unit struct {
		table, name string
		cfg         config.ProgramConfig
	}
	var units []unit
	for name, p := range cfg.Programs {
		units = append(units, unit{"programs", name, p})
	}
	// Pool members are spawned like programs (FUNC-055); their stdout carries
	// the protocol, so only stderr has a log file to check.
	for name, l := range cfg.EventListeners {
		p := l.ProgramConfig
		p.StdoutLogfile = ""
		units = append(units, unit{"eventlisteners", name, p})
	}
	slices.SortFunc(units, func(a, b unit) int {
		if a.table != b.table {
			return strings.Compare(b.table, a.table) // programs first
		}
		return strings.Compare(a.name, b.name)
	})

	var out []string
	seen := map[string]bool{}
	for _, u := range units {
		add := func(key string, err error) {
			msg := fmt.Sprintf("%s.%s.%s: %v", u.table, u.name, key, err)
			if !seen[msg] {
				seen[msg] = true
				out = append(out, msg)
			}
		}
		for _, inst := range ExpandNumprocs(u.name, u.cfg) {
			c := inst.Config
			dirOK := true
			if err := probeProgramDir(c.Directory); err != nil {
				add("directory", err)
				dirOK = false
			}
			if err := probeCommand(inst, dirOK); err != nil {
				add("command", err)
			}
			if c.StdoutLogfile != "" {
				if err := logging.ProbeLogFile(c.StdoutLogfile); err != nil {
					add("stdout_logfile", err)
				}
			}
			if c.StderrLogfile != "" && !c.RedirectStderr {
				if err := logging.ProbeLogFile(c.StderrLogfile); err != nil {
					add("stderr_logfile", err)
				}
			}
		}
	}
	return out
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
func probeCommand(inst ProcessInstance, dirOK bool) error {
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
			return lookPathIn(name, p.buildEnvFor(cred, true))
		}
	}
	return lookPathIn(name, p.buildEnvFor(userspec.Credential{}, false))
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

// lookPathIn resolves a bare name through the PATH in env, as exec.LookPath
// does with the trampoline's environment.
func lookPathIn(name string, env []string) error {
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		full := filepath.Join(dir, name)
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() && unix.Access(full, unix.X_OK) == nil {
			return nil
		}
	}
	return fmt.Errorf("exec: %q: executable file not found in $PATH", name)
}
