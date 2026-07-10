package config

import (
	"fmt"
	"os"
)

// DefaultSearchPaths is the ordered list of config file paths to try for
// convenience (non-daemon) lookups. It includes the current-directory
// ./kahi.toml.
var DefaultSearchPaths = []string{
	"./kahi.toml",
	"/etc/kahi/kahi.toml",
	"/etc/kahi.toml",
}

// DaemonSearchPaths is the ordered list of config file paths the daemon tries
// when no explicit path is given. Unlike DefaultSearchPaths it excludes the
// current-directory ./kahi.toml: a root daemon started without -c/KAHI_CONFIG
// from an attacker-writable directory must not load a planted config and run
// its commands as root (SEC-017). Explicit intent (-c / KAHI_CONFIG) is still
// trusted and may point at any path, including ./kahi.toml.
var DaemonSearchPaths = []string{
	"/etc/kahi/kahi.toml",
	"/etc/kahi.toml",
}

// Resolve finds the config file path for convenience (non-daemon) lookups by
// checking, in order:
//  1. Explicit path from -c flag (if non-empty)
//  2. KAHI_CONFIG environment variable
//  3. DefaultSearchPaths
//
// Returns the resolved path or an error.
func Resolve(explicit string) (string, error) {
	return resolve(explicit, DefaultSearchPaths)
}

// ResolveDaemon finds the config file path for the daemon by checking, in
// order:
//  1. Explicit path from -c flag (if non-empty)
//  2. KAHI_CONFIG environment variable
//  3. DaemonSearchPaths (system paths only, no current directory)
//
// It never selects a current-directory ./kahi.toml unless it was requested
// explicitly via -c or KAHI_CONFIG. Returns the resolved path or an error.
func ResolveDaemon(explicit string) (string, error) {
	return resolve(explicit, DaemonSearchPaths)
}

// resolve implements the shared resolution order over the given search paths.
func resolve(explicit string, searchPaths []string) (string, error) {
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

	for _, p := range searchPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("no config file found; searched %v", searchPaths)
}
