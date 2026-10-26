package config

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ResolveIncludes processes the include directive in the config,
// loading and merging all matched files. Returns warnings for patterns
// that match no files. The configDir is the directory of the main config file.
func ResolveIncludes(cfg *Config, configDir string) ([]string, error) {
	return resolveIncludes(cfg, configDir, make(map[string]bool))
}

// resolveIncludes merges cfg's includes. seen holds every file already
// loaded, so a file included twice, or an include cycle, is an error.
// Included files are expanded on their own, so %(here)s is the directory of
// the file that uses it, and their own includes resolve relative to them.
func resolveIncludes(cfg *Config, configDir string, seen map[string]bool) ([]string, error) {
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

			if seen[absPath] {
				return warnings, fmt.Errorf("circular include detected: %s", absPath)
			}
			seen[absPath] = true

			included, incWarnings, err := Load(absPath)
			if err != nil {
				return warnings, fmt.Errorf("include %s: %w", absPath, err)
			}
			warnings = append(warnings, incWarnings...)

			if err := ExpandVariables(included, absPath); err != nil {
				return warnings, fmt.Errorf("include %s: variable expansion failed: %w", absPath, err)
			}
			nested, err := resolveIncludes(included, filepath.Dir(absPath), seen)
			warnings = append(warnings, nested...)
			if err != nil {
				return warnings, err
			}

			// Merge programs from included files.
			if err := mergePrograms(cfg, included, absPath); err != nil {
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

func mergePrograms(dst, src *Config, srcPath string) error {
	for name, prog := range src.Programs {
		if existing, ok := dst.Programs[name]; ok {
			_ = existing
			return fmt.Errorf("duplicate program name %q: defined in both main config and %s", name, srcPath)
		}
		if dst.Programs == nil {
			dst.Programs = make(map[string]ProgramConfig)
		}
		dst.Programs[name] = prog
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

	// Resolve includes. The main file counts as seen, so a file that
	// includes it back is reported as a cycle.
	seen := make(map[string]bool)
	if abs, err := filepath.Abs(path); err == nil {
		seen[abs] = true
	}
	incWarnings, err := resolveIncludes(cfg, configDir, seen)
	warnings = append(warnings, incWarnings...)
	if err != nil {
		return nil, warnings, err
	}

	// Validate group references and process names across all files.
	if errs := append(validateGroupReferences(cfg), validateProcessNames(cfg)...); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return nil, warnings, fmt.Errorf("config validation failed:\n  %s",
			strings.Join(msgs, "\n  "))
	}

	warnings = append(warnings, stopWaitWarnings(cfg)...)
	warnings = append(warnings, ignoredSettingWarnings(cfg)...)

	return cfg, warnings, nil
}

// stopWaitWarnings flags programs whose stopwaitsecs exceeds
// shutdown_timeout: daemon shutdown SIGKILLs them before their own stop
// window ends. It runs after includes so included programs are covered.
func stopWaitWarnings(cfg *Config) []string {
	limit := cfg.Supervisor.ShutdownTimeout
	var names []string
	for name, p := range cfg.Programs {
		if p.Stopwaitsecs > limit {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var warnings []string
	for _, name := range names {
		warnings = append(warnings, fmt.Sprintf(
			"program %q: stopwaitsecs (%d) exceeds supervisor.shutdown_timeout (%d); daemon shutdown will SIGKILL it after %ds",
			name, cfg.Programs[name].Stopwaitsecs, limit, limit))
	}
	return warnings
}

func validateGroupReferences(cfg *Config) []error {
	var errs []error

	// Check group program references.
	for groupName, group := range cfg.Groups {
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
	for groupName, group := range cfg.Groups {
		for _, progName := range group.Programs {
			if existing, ok := progToGroup[progName]; ok {
				errs = append(errs, fmt.Errorf("program %q is in multiple groups: %s and %s", progName, existing, groupName))
			}
			progToGroup[progName] = groupName
		}
	}

	return errs
}
