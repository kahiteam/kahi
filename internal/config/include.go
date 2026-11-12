package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// ResolveIncludes processes the include directive in the config,
// loading and merging all matched files. Returns warnings for patterns
// that match no files. The configDir is the directory of the main config file.
func ResolveIncludes(cfg *Config, configDir string) ([]string, error) {
	return resolveIncludes(cfg, configDir, newIncludeState(""), mainConfigLabel, nil)
}

// mainConfigLabel names the main config file in error messages.
const mainConfigLabel = "main config"

// includeState is shared by one config load and all its includes.
type includeState struct {
	// loaded holds every file already merged: a file matched again, by a
	// second pattern or from another included file, is skipped.
	loaded map[string]bool
	// origins records, per merged config, the file each program came from,
	// so a duplicate names both files (#136).
	origins map[*Config]map[string]string
}

// newIncludeState starts the state for a load whose main file is mainPath
// ("" when it is not a file).
func newIncludeState(mainPath string) *includeState {
	st := &includeState{loaded: make(map[string]bool), origins: make(map[*Config]map[string]string)}
	if mainPath != "" {
		st.loaded[mainPath] = true
	}
	return st
}

// resolveIncludes merges cfg's includes into cfg. label names cfg's own file
// in messages, chain holds the files that include cfg, so a file that
// includes one of them is a cycle. Included files are expanded on their own,
// so %(here)s is the directory of the file that uses it, and their own
// includes resolve relative to them.
func resolveIncludes(cfg *Config, configDir string, st *includeState, label string, chain []string) ([]string, error) {
	if len(cfg.Include) == 0 {
		return nil, nil
	}

	var warnings []string

	for _, pattern := range cfg.Include {
		// Resolve relative patterns against config directory.
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(configDir, pattern)
		}

		matches, err := filepath.Glob(pattern)
		if err != nil {
			return warnings, fmt.Errorf("invalid include pattern %q: %w", pattern, err)
		}

		if len(matches) == 0 {
			warnings = append(warnings, fmt.Sprintf("include pattern %q matched no files", pattern))
			continue
		}

		// Sort for deterministic merge order.
		sort.Strings(matches)

		for _, path := range matches {
			absPath, err := filepath.Abs(path)
			if err != nil {
				return warnings, fmt.Errorf("cannot resolve include path %q: %w", path, err)
			}

			// A file that includes one of the files including it is a
			// cycle; one merged already, by another pattern or another
			// file, is merged once.
			if slices.Contains(chain, absPath) {
				return warnings, fmt.Errorf("circular include detected: %s", absPath)
			}
			if st.loaded[absPath] {
				warnings = append(warnings, fmt.Sprintf("include %s is matched more than once; merged once", absPath))
				continue
			}
			st.loaded[absPath] = true

			included, incWarnings, err := Load(absPath)
			if err != nil {
				return warnings, fmt.Errorf("include %s: %w", absPath, err)
			}
			warnings = append(warnings, incWarnings...)
			warnings = append(warnings, ignoredIncludeTables(absPath)...)

			if err := ExpandVariables(included, absPath); err != nil {
				return warnings, fmt.Errorf("include %s: variable expansion failed: %w", absPath, err)
			}
			nested, err := resolveIncludes(included, filepath.Dir(absPath), st, absPath, append(slices.Clone(chain), absPath))
			warnings = append(warnings, nested...)
			if err != nil {
				return warnings, err
			}

			// Merge programs from included files.
			if err := mergePrograms(cfg, included, label, absPath, st); err != nil {
				return warnings, err
			}

			// Merge groups from included files.
			mergeGroups(cfg, included)

			// Merge webhooks from included files.
			mergeWebhooks(cfg, included)
		}
	}

	// Clear includes to prevent re-processing.
	cfg.Include = nil

	return warnings, nil
}

// ignoredIncludeTables warns about the tables of an included file that are
// not merged: [supervisor] and [server.*] come from the main config only.
func ignoredIncludeTables(path string) []string {
	var raw map[string]any
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil // Load reported it already
	}
	var warnings []string
	if _, ok := raw["supervisor"]; ok {
		warnings = append(warnings, fmt.Sprintf("%s: [supervisor] in an included file is ignored; set it in the main config", path))
	}
	if server, ok := raw["server"].(map[string]any); ok {
		tables := make([]string, 0, len(server))
		for name := range server {
			tables = append(tables, name)
		}
		sort.Strings(tables)
		for _, name := range tables {
			warnings = append(warnings, fmt.Sprintf("%s: [server.%s] in an included file is ignored; set it in the main config", path, name))
		}
	}
	return warnings
}

// mergePrograms merges src's programs into dst. dstLabel names dst's own
// file and srcPath src's; a program defined twice is an error naming both
// files it came from.
func mergePrograms(dst, src *Config, dstLabel, srcPath string, st *includeState) error {
	if st.origins[dst] == nil {
		st.origins[dst] = make(map[string]string)
	}
	for name, prog := range src.Programs {
		from := st.origins[src][name]
		if from == "" {
			from = srcPath
		}
		if _, ok := dst.Programs[name]; ok {
			other := st.origins[dst][name]
			if other == "" {
				other = dstLabel
			}
			return fmt.Errorf("duplicate program name %q: defined in both %s and %s", name, other, from)
		}
		if dst.Programs == nil {
			dst.Programs = make(map[string]ProgramConfig)
		}
		dst.Programs[name] = prog
		st.origins[dst][name] = from
	}
	return nil
}

func mergeGroups(dst, src *Config) {
	for name, group := range src.Groups {
		if dst.Groups == nil {
			dst.Groups = make(map[string]GroupConfig)
		}
		// Later definitions win for groups.
		dst.Groups[name] = group
	}
}

func mergeWebhooks(dst, src *Config) {
	for name, wh := range src.Webhooks {
		if dst.Webhooks == nil {
			dst.Webhooks = make(map[string]WebhookConfig)
		}
		dst.Webhooks[name] = wh
	}
}

// LoadWithIncludes loads a config file and resolves all includes.
func LoadWithIncludes(path string) (*Config, []string, error) {
	cfg, warnings, err := Load(path)
	if err != nil {
		return nil, warnings, err
	}

	configDir := filepath.Dir(path)

	// Expand variables before processing includes.
	if err := ExpandVariables(cfg, path); err != nil {
		return nil, warnings, fmt.Errorf("variable expansion failed: %w", err)
	}

	// Resolve includes. The main file counts as loaded and starts the
	// chain, so a file that includes it back is reported as a cycle.
	absMain, _ := filepath.Abs(path)
	var chain []string
	if absMain != "" {
		chain = []string{absMain}
	}
	incWarnings, err := resolveIncludes(cfg, configDir, newIncludeState(absMain), mainConfigLabel, chain)
	warnings = append(warnings, incWarnings...)
	if err != nil {
		return nil, warnings, err
	}

	// Validate group references and process names across all files.
	if errs := ValidateMerged(cfg); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return nil, warnings, fmt.Errorf("config validation failed:\n  %s",
			strings.Join(msgs, "\n  "))
	}

	warnings = append(warnings, shutdownWarnings(cfg)...)
	warnings = append(warnings, ignoredSettingWarnings(cfg)...)

	return cfg, warnings, nil
}

// OrderKey is a program's ordering key without the name (FUNC-008): the
// priority of the [groups] table that lists it, else its own priority, then
// its own priority. Programs with the same key form one shutdown level.
func OrderKey(cfg *Config, prog string) (group, own int) {
	own = cfg.Programs[prog].Priority
	for _, g := range cfg.Groups {
		if slices.Contains(g.Programs, prog) {
			return g.Priority, own
		}
	}
	return own, own
}

// shutdownWarnings flags a config whose ordered shutdown can outlast
// shutdown_timeout: levels stop one after another, each for up to its
// largest stopwaitsecs (FUNC-066, ADR-031). It runs after includes so
// included programs are covered.
func shutdownWarnings(cfg *Config) []string {
	type key struct{ group, own int }
	largest := make(map[key]int)
	for name, p := range cfg.Programs {
		g, o := OrderKey(cfg, name)
		k := key{g, o}
		largest[k] = max(largest[k], p.Stopwaitsecs)
	}
	sum := 0
	for _, w := range largest {
		sum += w
	}
	limit := cfg.Supervisor.ShutdownTimeout
	if sum <= limit {
		return nil
	}
	return []string{fmt.Sprintf("ordered shutdown can take up to %ds (largest stopwaitsecs per priority level, summed), "+
		"more than supervisor.shutdown_timeout (%ds); processes still running then are SIGKILLed", sum, limit)}
}

// ValidateMerged runs the checks that need the whole config, after includes
// are merged: group references and process names. Validate covers each
// file on its own. The daemon runs both through LoadWithIncludes; anything
// else that accepts a config on the daemon's behalf, such as kahi migrate,
// must run both too (#245).
func ValidateMerged(cfg *Config) []error {
	return append(validateGroupReferences(cfg), validateProcessNames(cfg)...)
}

func validateGroupReferences(cfg *Config) []error {
	var errs []error

	// Sorted, so the errors and the group named first are deterministic.
	groupNames := make([]string, 0, len(cfg.Groups))
	for name := range cfg.Groups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)

	for _, groupName := range groupNames {
		group := cfg.Groups[groupName]
		// A [groups] table named like a program would replace that
		// program's own group (FUNC-009, #214).
		if _, ok := cfg.Programs[groupName]; ok {
			errs = append(errs, fmt.Errorf("groups.%s: name collides with program %s", groupName, groupName))
		}
		if len(group.Programs) == 0 {
			errs = append(errs, fmt.Errorf("groups.%s: empty group (no programs)", groupName))
		}
		for _, progName := range group.Programs {
			if _, ok := cfg.Programs[progName]; !ok {
				errs = append(errs, fmt.Errorf("groups.%s: references nonexistent program %q", groupName, progName))
			}
		}
	}

	// Check for programs in multiple explicit groups.
	progToGroup := make(map[string]string)
	for _, groupName := range groupNames {
		for _, progName := range cfg.Groups[groupName].Programs {
			if existing, ok := progToGroup[progName]; ok {
				errs = append(errs, fmt.Errorf("program %q is in multiple groups: %s and %s", progName, existing, groupName))
			}
			progToGroup[progName] = groupName
		}
	}

	return errs
}
