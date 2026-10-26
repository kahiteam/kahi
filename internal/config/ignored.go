package config

import (
	"fmt"
	"sort"
	"strings"
)

// ignoredSettingWarnings flags settings that parse and validate but that
// nothing in the daemon acts on yet (#106, #113), so a user does not rely on
// them silently. Each warning names the setting and says it is ignored.
func ignoredSettingWarnings(cfg *Config) []string {
	var warnings []string
	ignored := func(setting string) {
		warnings = append(warnings, fmt.Sprintf("%s is not implemented yet and is ignored", setting))
	}

	if len(cfg.Webhooks) > 0 {
		names := make([]string, 0, len(cfg.Webhooks))
		for name := range cfg.Webhooks {
			names = append(names, name)
		}
		sort.Strings(names)
		ignored(fmt.Sprintf("[webhooks] (%s): no webhook is delivered", strings.Join(names, ", ")))
	}

	s := cfg.Supervisor
	if s.Directory != "" {
		ignored("supervisor.directory")
	}
	// minfds and minprocs have defaults; only a different value was set on
	// purpose.
	if s.Minfds != 1024 {
		ignored(fmt.Sprintf("supervisor.minfds = %d", s.Minfds))
	}
	if s.Minprocs != 200 {
		ignored(fmt.Sprintf("supervisor.minprocs = %d", s.Minprocs))
	}
	if s.Nocleanup {
		ignored("supervisor.nocleanup")
	}
	if s.Identifier != "" {
		ignored("supervisor.identifier")
	}

	names := make([]string, 0, len(cfg.Programs))
	for name := range cfg.Programs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := cfg.Programs[name]
		if p.StdoutCaptureMaxbytes != "" {
			ignored(fmt.Sprintf("programs.%s.stdout_capture_maxbytes (the capture buffer is fixed at 64 KB)", name))
		}
		if p.StderrCaptureMaxbytes != "" {
			ignored(fmt.Sprintf("programs.%s.stderr_capture_maxbytes (the capture buffer is fixed at 64 KB)", name))
		}
		if p.StdoutSyslog {
			ignored(fmt.Sprintf("programs.%s.stdout_syslog", name))
		}
		if p.StderrSyslog {
			ignored(fmt.Sprintf("programs.%s.stderr_syslog", name))
		}
	}
	return warnings
}
