package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Load reads a TOML config file, applies defaults, validates, and returns
// the config along with any warnings (e.g. unknown fields).
func Load(path string) (*Config, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read config: %s: %w", path, err)
	}

	return LoadBytes(data, path)
}

// LoadBytes parses TOML from raw bytes. The path argument is used only for
// error messages.
func LoadBytes(data []byte, path string) (*Config, []string, error) {
	var cfg Config
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("config parse error in %s: %w", path, err)
	}

	// Collect warnings for unknown fields.
	var warnings []string
	for _, key := range md.Undecoded() {
		warnings = append(warnings, fmt.Sprintf("unknown config key: %s", strings.Join(key, ".")))
	}

	ApplyDefaults(&cfg)

	if errs := Validate(&cfg); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return nil, warnings, fmt.Errorf("config validation failed in %s:\n  %s",
			path, strings.Join(msgs, "\n  "))
	}

	return &cfg, warnings, nil
}
