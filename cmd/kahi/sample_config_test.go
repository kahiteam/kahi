package main

import (
	"os"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

// The shipped sample (cmd/kahi/kahi.toml, in every release archive) and the
// README's sample config are copies of the kahi init template; they drifted
// apart once (#139), so this test pins them to it.
func TestSampleConfigsMatchTemplate(t *testing.T) {
	shipped, err := os.ReadFile("kahi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if string(shipped) != config.DefaultConfigTOML {
		t.Error("cmd/kahi/kahi.toml differs from the kahi init template; regenerate it with: go run ./cmd/kahi init --stdout > cmd/kahi/kahi.toml")
	}

	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	// The README shows the template without its two header comment lines.
	_, body, ok := strings.Cut(config.DefaultConfigTOML, "documentation.\n\n")
	if !ok {
		t.Fatal("template header changed; update this test")
	}
	if !strings.Contains(string(readme), "```toml\n"+body+"```") {
		t.Error("README's sample config differs from the kahi init template; copy the template body into the Sample config section")
	}
}
