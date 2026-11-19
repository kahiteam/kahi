package supervisor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"

	"github.com/kahiteam/kahi/internal/config"
)

// Config access after a privilege drop (FUNC-045, decided 2026-11-19): the
// daemon reads the config as root only at startup; reload, update, reread and
// avail read it as the drop user.

// runningAs names the effective user: "{name} (uid {uid})", or "uid {uid}"
// without a passwd entry.
func runningAs() string {
	uid := strconv.Itoa(os.Geteuid())
	if u, err := user.LookupId(uid); err == nil && u.Username != "" {
		return u.Username + " (uid " + uid + ")"
	}
	return "uid " + uid
}

// runningGroup is the effective primary group name, or its gid.
func runningGroup() string {
	gid := strconv.Itoa(os.Getegid())
	if g, err := user.LookupGroupId(gid); err == nil && g.Name != "" {
		return g.Name
	}
	return gid
}

// loadConfig loads the config file with includes, as every read after start
// does. A permission error says what to do about it when the daemon is not
// root.
func (s *Supervisor) loadConfig() (*config.Config, []string, error) {
	cfg, warnings, err := config.LoadWithIncludes(s.configPath)
	if err != nil && os.Geteuid() != 0 && errors.Is(err, fs.ErrPermission) {
		who := runningAs()
		err = fmt.Errorf("%w; the daemon runs as %s: restart it to apply config changes, or make the file readable by %s", err, who, who)
	}
	return cfg, warnings, err
}

// CheckConfigAfterDrop loads the config at path once more, as the user the
// daemon dropped to, and returns a warning when a later reload would fail or
// read a different config than the one started (started); "" when it reads
// the same. Call it right after DropPrivileges.
func CheckConfigAfterDrop(path string, started *config.Config) string {
	who := runningAs()
	cfg, _, err := config.LoadWithIncludes(path)
	if err != nil {
		if !errors.Is(err, fs.ErrPermission) {
			// Not an access problem: a later reload reports it as usual.
			return ""
		}
		file := path
		var pe *fs.PathError
		if errors.As(err, &pe) {
			file = pe.Path
		}
		return fmt.Sprintf("config not readable as %s after the privilege drop: %v; reload, update, reread and avail will fail until the daemon is restarted or the file is readable by %s (for example chgrp %s %s; chmod 0640 %s)",
			who, err, who, runningGroup(), file, file)
	}
	if missing := missingNames(started, cfg); len(missing) > 0 {
		return fmt.Sprintf("config reads differently as %s after the privilege drop (missing: %s); an include directory is probably not readable by %s",
			who, strings.Join(missing, ", "), who)
	}
	return ""
}

// missingNames lists the programs, event listener pools and groups of a
// that b lacks, sorted.
func missingNames(a, b *config.Config) []string {
	var out []string
	for name := range a.Programs {
		if _, ok := b.Programs[name]; !ok {
			out = append(out, name)
		}
	}
	for name := range a.EventListeners {
		if _, ok := b.EventListeners[name]; !ok {
			out = append(out, name)
		}
	}
	for name := range a.Groups {
		if _, ok := b.Groups[name]; !ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
