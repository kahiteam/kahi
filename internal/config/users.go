package config

import (
	"fmt"
	"sort"

	"github.com/kahiteam/kahi/internal/userspec"
)

// resolveUser is the user resolver, replaceable in tests (ADR-046).
var resolveUser = userspec.Resolve

// userWarnings flags a per-process user that does not resolve now. It is a
// warning, not an error: the user is looked up again at each spawn, so one
// created later is picked up then (FUNC-046).
func userWarnings(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Programs))
	for name := range cfg.Programs {
		names = append(names, name)
	}
	sort.Strings(names)
	var warnings []string
	for _, name := range names {
		u := cfg.Programs[name].User
		if u == "" {
			continue
		}
		if _, err := resolveUser(u); err != nil {
			warnings = append(warnings, fmt.Sprintf("programs.%s.user: %v", name, err))
		}
	}
	for _, name := range listenerNames(cfg) {
		if u := cfg.EventListeners[name].User; u != "" {
			if _, err := resolveUser(u); err != nil {
				warnings = append(warnings, fmt.Sprintf("eventlisteners.%s.user: %v", name, err))
			}
		}
	}
	return warnings
}
