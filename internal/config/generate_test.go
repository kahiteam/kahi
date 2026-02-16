package config

import (
	"strings"
	"testing"
)

func TestDefaultConfigIsValidTOML(t *testing.T) {
	cfg, _, err := LoadBytes([]byte(DefaultConfigTOML), "generated")
	if err != nil {
		t.Fatalf("generated config is invalid TOML: %v", err)
	}
	// Should have no programs defined
	if len(cfg.Programs) != 0 {
		t.Errorf("expected 0 programs, got %d", len(cfg.Programs))
	}
}

func TestDefaultConfigContainsAllSections(t *testing.T) {
	for _, section := range []string{
		"[supervisor]",
		"[server.unix]",
		"[server.http]",
		"[programs.example]",
		"[groups.services]",
		"[webhooks.slack]",
	} {
		if !strings.Contains(DefaultConfigTOML, section) {
			t.Errorf("missing section %q in generated config", section)
		}
	}
}
