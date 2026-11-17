package config

import "sort"

// ReloadResult is the outcome of a config reload, or of a preview of one:
// the programs added, changed and removed, and the warnings the new config
// produced (unknown keys, ignored settings), so a caller can show them.
type ReloadResult struct {
	Added    []string
	Changed  []string
	Removed  []string
	Warnings []string
}

// AvailEntry is one program or event listener pool in the config file, for
// kahi ctl avail (FUNC-085): its group (the [groups] table, or its own
// name), whether it is in the running state, its autostart and priority.
type AvailEntry struct {
	Name      string `json:"name"`
	Group     string `json:"group"`
	InUse     bool   `json:"in_use"`
	Autostart bool   `json:"autostart"`
	Priority  int    `json:"priority"`
}

// GroupOf returns the [groups] table that lists prog, or prog itself.
func GroupOf(cfg *Config, prog string) string {
	names := make([]string, 0, len(cfg.Groups))
	for name := range cfg.Groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, p := range cfg.Groups[name].Programs {
			if p == prog {
				return name
			}
		}
	}
	return prog
}
