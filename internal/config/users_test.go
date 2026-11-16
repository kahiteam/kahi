package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/userspec"
)

func TestLoadWarnsUnknownProgramUser(t *testing.T) {
	orig := resolveUser
	resolveUser = func(spec string) (userspec.Credential, error) {
		if spec == "kahi" || spec == "1000" {
			return userspec.Credential{Uid: 1000, Gid: 1000}, nil
		}
		if spec == "kahi:nosuch" {
			return userspec.Credential{}, errors.New("group not found: nosuch")
		}
		return userspec.Credential{}, errors.New("user not found: " + spec)
	}
	t.Cleanup(func() { resolveUser = orig })

	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte(`[programs.a]
command = "/bin/true"
user = "kahi"

[programs.b]
command = "/bin/true"
user = "nosuchuser"

[programs.c]
command = "/bin/true"
user = "kahi:nosuch"

[programs.d]
command = "/bin/true"
`), 0644); err != nil {
		t.Fatal(err)
	}
	_, warnings, err := LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"programs.b.user: user not found: nosuchuser", "programs.c.user: group not found: nosuch"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no warning %q; warnings:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "programs.a.user") || strings.Contains(joined, "programs.d.user") {
		t.Errorf("unexpected user warning; warnings:\n%s", joined)
	}
}
