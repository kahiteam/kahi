package process

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/logging"
	"github.com/kahiteam/kahi/internal/userspec"
)

// Program checks as the user a program runs as (FUNC-087, #293, decided
// 2026-11-20). kahi config check run as root uses root's access rights, but
// after a privilege drop the daemon opens program logs and spawns programs
// as the drop user, and a program's user setting runs it as that user.
// These checks evaluate the permission bits along each path for that user
// (owner, group, other; search permission on every directory). ACLs are
// not evaluated. Nothing is spawned.

// Permission bits as in the mode's rwx triplets.
const (
	permX = 1
	permW = 2
)

// ProgramUsers says, for the as-user checks, who runs programs: Drop is the
// user the daemon drops to (it also opens program logs), nil when there is
// no drop; ProgramUser resolves a program's user setting, which applies only
// without a drop (the dropped daemon cannot switch users).
type ProgramUsers struct {
	Drop        *userspec.Credential
	ProgramUser func(spec string) (userspec.Credential, error)
}

// ProbeProgramsAs returns the warnings for paths the program's user cannot
// use although the check's own user can. Call it only when the check runs
// as root; otherwise ProbePrograms already used the right user's rights.
func ProbeProgramsAs(cfg *config.Config, relOK bool, users ProgramUsers) []string {
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
		skip := func(path string) bool { return path == "" || (!relOK && !filepath.IsAbs(path)) }
		for _, inst := range ExpandNumprocs(u.name, u.cfg) {
			c := inst.Config
			execAs := users.Drop
			if execAs == nil && c.User != "" && users.ProgramUser != nil {
				if cred, err := users.ProgramUser(c.User); err == nil {
					execAs = &cred
				}
			}
			if execAs != nil && execAs.Uid != 0 {
				if !skip(c.Directory) && dirExists(c.Directory) {
					if err := accessAs(c.Directory, *execAs, permX); err != nil {
						add("directory", fmt.Errorf("cannot access directory: %s: permission denied (as %s)", c.Directory, label(*execAs)))
					}
				}
				if path, display := commandPath(c); path != "" && !skip(path) && fileExists(path) {
					if err := accessAs(path, *execAs, permX); err != nil {
						add("command", fmt.Errorf("%s: permission denied (as %s)", display, label(*execAs)))
					}
				}
			}
			// Program logs are opened by the daemon: as the drop user after a
			// drop, otherwise as root.
			if users.Drop == nil || users.Drop.Uid == 0 {
				continue
			}
			for _, lf := range []struct{ key, path string }{
				{"stdout_logfile", c.StdoutLogfile},
				{"stderr_logfile", stderrLogfile(c)},
			} {
				if skip(lf.path) || logging.ProbeLogFile(lf.path) != nil {
					continue // missing or refused for root too: ProbePrograms reports it
				}
				if err := logWritableAs(lf.path, *users.Drop); err != nil {
					add(lf.key, fmt.Errorf("%s: permission denied (as %s)", lf.path, label(*users.Drop)))
				}
			}
		}
	}
	return out
}

func stderrLogfile(c config.ProgramConfig) string {
	if c.RedirectStderr {
		return ""
	}
	return c.StderrLogfile
}

// commandPath is the file the spawn would exec for a command with a slash
// (relative to the program's directory) or a bare name found in the
// supervisor's PATH; "" when it is not resolved here.
func commandPath(c config.ProgramConfig) (path, display string) {
	parts := strings.Fields(c.Command)
	if len(parts) == 0 {
		return "", ""
	}
	name := parts[0]
	if strings.Contains(name, "/") {
		if !filepath.IsAbs(name) && c.Directory != "" {
			return filepath.Join(c.Directory, name), name
		}
		return name, name
	}
	if p, err := exec.LookPath(name); err == nil && filepath.IsAbs(p) {
		return p, name
	}
	return "", ""
}

// logWritableAs reports whether cred can open path for appending: an
// existing file must be writable, a missing one needs a writable parent.
func logWritableAs(path string, cred userspec.Credential) error {
	if fileExists(path) {
		return accessAs(path, cred, permW)
	}
	return accessAs(filepath.Dir(path), cred, permW|permX)
}

// accessAs reports whether cred may use path with the want bits, by the
// permission bits alone: every directory on the way needs search (x)
// permission, the path itself want. Symlinks are followed, as the kernel
// follows them.
func accessAs(path string, cred userspec.Credential, want uint32) error {
	if cred.Uid == 0 {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dir := "/"
	for _, c := range strings.Split(filepath.Dir(abs), "/") {
		if c == "" {
			continue
		}
		dir = filepath.Join(dir, c)
		if err := permitted(dir, cred, permX); err != nil {
			return err
		}
	}
	return permitted(abs, cred, want)
}

func permitted(path string, cred userspec.Credential, want uint32) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	mode := uint32(fi.Mode().Perm())
	var bits uint32
	switch {
	case st.Uid == cred.Uid:
		bits = mode >> 6
	case st.Gid == cred.Gid || slices.Contains(cred.Groups, st.Gid):
		bits = mode >> 3
	default:
		bits = mode
	}
	if bits&7&want != want {
		return &fs.PathError{Op: "access", Path: path, Err: fs.ErrPermission}
	}
	return nil
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// label names a user as the other drop-user messages do: "{name} (uid
// {uid})", or "uid {uid}" without a passwd entry.
func label(c userspec.Credential) string {
	if c.Name != "" {
		return fmt.Sprintf("%s (uid %d)", c.Name, c.Uid)
	}
	return fmt.Sprintf("uid %d", c.Uid)
}
