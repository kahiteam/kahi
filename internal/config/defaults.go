package config

import (
	"fmt"
	"strconv"

	"github.com/BurntSushi/toml"
)

// ApplyDefaults fills in unset fields with their default values. For the
// numeric settings where 0 is meaningful (priority 0 starts first,
// startretries 0 never retries, backups 0 keeps none, exitcodes [] expects
// no exit), a field gets its default only when the TOML does not set the
// key, so an explicit 0 is kept (#138). Validation then rejects the zeros
// that are unsafe, such as stopwaitsecs = 0. md may be empty for a config
// built in code; zero values are then defaulted as before.
func ApplyDefaults(cfg *Config, md toml.MetaData) {
	// unset reports whether a field with zero value v was left out of the
	// TOML, so it should get its default.
	unset := func(v int, key ...string) bool { return v == 0 && !md.IsDefined(key...) }

	// Supervisor defaults.
	if cfg.Supervisor.LogLevel == "" {
		cfg.Supervisor.LogLevel = "info"
	}
	if cfg.Supervisor.LogFormat == "" {
		cfg.Supervisor.LogFormat = "json"
	}
	cfg.Supervisor.MinfdsSet = md.IsDefined("supervisor", "minfds")
	cfg.Supervisor.MinprocsSet = md.IsDefined("supervisor", "minprocs")
	if cfg.Supervisor.Minfds == 0 {
		cfg.Supervisor.Minfds = 1024
	}
	if cfg.Supervisor.Minprocs == 0 {
		cfg.Supervisor.Minprocs = 200
	}
	if unset(cfg.Supervisor.ShutdownTimeout, "supervisor", "shutdown_timeout") {
		cfg.Supervisor.ShutdownTimeout = 30
	}

	// Webhooks: timeout 5, max_retries 3, both kept when set (an explicit
	// 0 is refused or means no retries). retries is the old name.
	for name, w := range cfg.Webhooks {
		if unset(w.Timeout, "webhooks", name, "timeout") {
			w.Timeout = 5
		}
		w.RetriesSet = md.IsDefined("webhooks", name, "retries")
		if !md.IsDefined("webhooks", name, "max_retries") {
			if w.RetriesSet {
				w.MaxRetries = w.Retries
			} else if w.MaxRetries == 0 {
				w.MaxRetries = 3
			}
		}
		cfg.Webhooks[name] = w
	}

	// Server defaults.
	if cfg.Server.Unix.File == "" {
		cfg.Server.Unix.File = DefaultSocketPath()
	}
	if cfg.Server.Unix.Chmod == "" {
		cfg.Server.Unix.Chmod = "0700"
	}

	cfg.Supervisor.Umask = canonicalUmask(cfg.Supervisor.Umask)

	// Program defaults.
	for name, p := range cfg.Programs {
		// "022" and "0022" are the same umask; one spelling keeps a reload
		// from seeing a change (FUNC-047).
		p.Umask = canonicalUmask(p.Umask)
		if unset(p.Numprocs, "programs", name, "numprocs") {
			p.Numprocs = 1
		}
		if unset(p.Priority, "programs", name, "priority") {
			p.Priority = 999
		}
		if p.Autostart == nil {
			t := true
			p.Autostart = &t
		}
		if p.Autorestart == "" {
			p.Autorestart = "unexpected"
		}
		if unset(p.Startsecs, "programs", name, "startsecs") {
			p.Startsecs = 1
		}
		if unset(p.Startretries, "programs", name, "startretries") {
			p.Startretries = 3
		}
		if len(p.Exitcodes) == 0 && !md.IsDefined("programs", name, "exitcodes") {
			p.Exitcodes = []int{0}
		}
		if p.Stopsignal == "" {
			p.Stopsignal = "SIGTERM"
		}
		if unset(p.Stopwaitsecs, "programs", name, "stopwaitsecs") {
			p.Stopwaitsecs = 10
		}
		if p.StdoutLogfileMaxbytes == "" {
			p.StdoutLogfileMaxbytes = "50MB"
		}
		if unset(p.StdoutLogfileBackups, "programs", name, "stdout_logfile_backups") {
			p.StdoutLogfileBackups = 10
		}
		if p.StderrLogfileMaxbytes == "" {
			p.StderrLogfileMaxbytes = "50MB"
		}
		if unset(p.StderrLogfileBackups, "programs", name, "stderr_logfile_backups") {
			p.StderrLogfileBackups = 10
		}
		cfg.Programs[name] = p
	}

	// Group defaults: an unset priority is 999 like a program's, so a group
	// that sets none does not move its members ahead (#196).
	for name, g := range cfg.Groups {
		if unset(g.Priority, "groups", name, "priority") {
			g.Priority = 999
		}
		cfg.Groups[name] = g
	}
}

// canonicalUmask writes a valid umask with 4 digits; anything else is left
// for validation to report.
func canonicalUmask(s string) string {
	if !ValidUmask(s) {
		return s
	}
	v, _ := strconv.ParseUint(s, 8, 32)
	return fmt.Sprintf("%04o", v)
}
