package config

// ReloadResult is the outcome of a config reload, or of a preview of one:
// the programs added, changed and removed, and the warnings the new config
// produced (unknown keys, ignored settings), so a caller can show them.
type ReloadResult struct {
	Added    []string
	Changed  []string
	Removed  []string
	Warnings []string
}
