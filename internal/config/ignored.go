package config

import (
	"fmt"
	"sort"
)

// ignoredSettingWarnings flags settings that parse and validate but that
// nothing in the daemon acts on yet (#106, #113), so a user does not rely on
// them silently. Each warning names the setting and says it is ignored.
func ignoredSettingWarnings(cfg *Config) []string {
	var warnings []string
	ignored := func(setting string) {
		warnings = append(warnings, fmt.Sprintf("%s is not implemented yet and is ignored", setting))
	}

	s := cfg.Supervisor
	// FUNC-023 is withdrawn: kahi never names child logs itself, so there
	// is nothing to clean up.
	if s.Nocleanup {
		warnings = append(warnings, "supervisor.nocleanup has no effect: kahi does not auto-name child logs")
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
		if p.StdoutSyslog {
			ignored(fmt.Sprintf("programs.%s.stdout_syslog", name))
		}
		if p.StderrSyslog {
			ignored(fmt.Sprintf("programs.%s.stderr_syslog", name))
		}
	}
	return warnings
}

// redirectWarnings flags stderr settings that have no effect because
// redirect_stderr sends stderr to the stdout stream (FUNC-051).
func redirectWarnings(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Programs))
	for name := range cfg.Programs {
		names = append(names, name)
	}
	sort.Strings(names)
	var warnings []string
	for _, name := range names {
		p := cfg.Programs[name]
		if p.RedirectStderr && p.StderrEventsEnabled {
			warnings = append(warnings, fmt.Sprintf("programs.%s.stderr_events_enabled has no effect with redirect_stderr", name))
		}
		if p.RedirectStderr && p.StderrCaptureMaxbytes != "" {
			warnings = append(warnings, fmt.Sprintf("programs.%s.stderr_capture_maxbytes has no effect with redirect_stderr", name))
		}
	}
	return warnings
}
