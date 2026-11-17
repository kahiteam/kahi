package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kahiteam/kahi/internal/events"
)

// listenerStdoutKeys are the program keys about stdout output, which has no
// effect on a pool: its stdout carries the listener protocol (FUNC-055).
var listenerStdoutKeys = []string{
	"stdout_logfile", "stdout_logfile_maxbytes", "stdout_logfile_backups",
	"stdout_syslog", "stdout_events_enabled", "stdout_capture_maxbytes",
}

func listenerNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.EventListeners))
	for name := range cfg.EventListeners {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// validateEventListeners checks each pool as a program plus its own keys
// (FUNC-055, ADR-042).
func validateEventListeners(cfg *Config) []error {
	var errs []error
	for _, name := range listenerNames(cfg) {
		l := cfg.EventListeners[name]
		prefix := "eventlisteners." + name
		errs = append(errs, checkProgram(prefix, l.ProgramConfig)...)
		if l.BufferSize < 1 {
			errs = append(errs, fmt.Errorf("%s.buffer_size must be >= 1", prefix))
		}
		if l.ResultTimeout < 1 {
			errs = append(errs, fmt.Errorf("%s.result_timeout must be >= 1", prefix))
		}
		if l.RedirectStderr {
			errs = append(errs, fmt.Errorf("%s.redirect_stderr: not allowed, stdout carries the protocol", prefix))
		}
	}
	return errs
}

// validateListenerNames runs after includes: pools share the program
// namespace, and a pool's group is its own name, so it cannot be listed in a
// [groups] table nor share a name with one (ADR-036, ADR-042).
func validateListenerNames(cfg *Config) []error {
	var errs []error
	for _, name := range listenerNames(cfg) {
		if _, ok := cfg.Programs[name]; ok {
			errs = append(errs, fmt.Errorf("eventlisteners.%s: name already used by programs.%s", name, name))
		}
		if _, ok := cfg.Groups[name]; ok {
			errs = append(errs, fmt.Errorf("groups.%s: name collides with event listener pool %s", name, name))
		}
	}
	groups := make([]string, 0, len(cfg.Groups))
	for g := range cfg.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		for _, member := range cfg.Groups[g].Programs {
			if _, ok := cfg.EventListeners[member]; ok {
				errs = append(errs, fmt.Errorf("groups.%s: %s is an event listener pool; a pool cannot be in a group", g, member))
			}
		}
	}
	return errs
}

// listenerWarnings flags unknown or missing event types (a warning, not an
// error: a typo must not keep the daemon from starting, decided 2026-11-16)
// and stdout output keys, which have no effect on a pool.
func listenerWarnings(cfg *Config) []string {
	var warnings []string
	for _, name := range listenerNames(cfg) {
		l := cfg.EventListeners[name]
		if len(l.Events) == 0 {
			warnings = append(warnings, fmt.Sprintf("eventlisteners.%s: no events; the pool never receives any", name))
		}
		for _, e := range l.Events {
			if !knownEvent(e) {
				warnings = append(warnings, fmt.Sprintf("eventlisteners.%s.events: unknown event type: %s (valid: %s); ignored", name, e, allEventNames()))
			}
		}
		for _, key := range l.StdoutKeysSet {
			warnings = append(warnings, fmt.Sprintf("eventlisteners.%s.%s has no effect: stdout carries the protocol", name, key))
		}
	}
	return warnings
}

// mergeEventListeners merges src's pools into dst like programs: a pool
// defined twice is an error naming both files.
func mergeEventListeners(dst, src *Config, dstLabel, srcPath string, st *includeState) error {
	if st.origins[dst] == nil {
		st.origins[dst] = make(map[string]string)
	}
	for name, l := range src.EventListeners {
		key := "eventlisteners." + name
		from := st.origins[src][key]
		if from == "" {
			from = srcPath
		}
		if _, ok := dst.EventListeners[name]; ok {
			other := st.origins[dst][key]
			if other == "" {
				other = dstLabel
			}
			return fmt.Errorf("duplicate event listener name %q: defined in both %s and %s", name, other, from)
		}
		if dst.EventListeners == nil {
			dst.EventListeners = make(map[string]EventListenerConfig)
		}
		dst.EventListeners[name] = l
		st.origins[dst][key] = from
	}
	return nil
}

// allEventNames lists every event type a pool can subscribe to.
func allEventNames() string {
	names := make([]string, 0, 16)
	for _, t := range events.AllEventTypes {
		names = append(names, string(t))
	}
	return strings.Join(names, ", ")
}
