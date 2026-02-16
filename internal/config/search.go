package config

import (
	"fmt"
	"os"
)

// DefaultSearchPaths is the ordered list of config file paths to try.
var DefaultSearchPaths = []string{
	"./kahi.toml",
	"/etc/kahi/kahi.toml",
	"/etc/kahi.toml",
}

// Resolve finds the config file path by checking, in order:
//  1. Explicit path from -c flag (if non-empty)
//  2. KAHI_CONFIG environment variable
//  3. DefaultSearchPaths
//
// Returns the resolved path or an error.
func Resolve(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("cannot read config: %s: %w", explicit, err)
		}
		return explicit, nil
	}

	if env := os.Getenv("KAHI_CONFIG"); env != "" {
		if _, err := os.Stat(env); err != nil {
			return "", fmt.Errorf("cannot read config: %s: %w", env, err)
		}
		return env, nil
	}

	for _, p := range DefaultSearchPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("no config file found; searched %v", DefaultSearchPaths)
}
