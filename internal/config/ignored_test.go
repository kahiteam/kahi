package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Settings that nothing acts on yet must each produce a warning (#106,
// #113), so a user is not misled into relying on them.
func TestLoadWithIncludesWarnsIgnoredSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte(`[supervisor]
directory = "/srv"
minfds = 4096
minprocs = 500
nocleanup = true

[programs.web]
command = "/bin/web"
stdout_capture_maxbytes = "1MB"
stderr_capture_maxbytes = "1MB"
stdout_syslog = true
stderr_syslog = true

[webhooks.slack]
url = "https://hooks.example.com/x"
events = ["process_state"]
`), 0644); err != nil {
		t.Fatal(err)
	}

	_, warnings, err := LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{
		"[webhooks] (slack)",
		"supervisor.directory",
		"supervisor.minfds = 4096",
		"supervisor.minprocs = 500",
		"supervisor.nocleanup",
		"programs.web.stdout_capture_maxbytes",
		"programs.web.stderr_capture_maxbytes",
		"programs.web.stdout_syslog",
		"programs.web.stderr_syslog",
	} {
		if !strings.Contains(joined, want+" ") && !strings.Contains(joined, want+":") {
			t.Errorf("no ignored-setting warning for %s; warnings:\n%s", want, joined)
		}
	}
}

// The generated sample config sets none of them, so it loads without
// ignored-setting warnings.
func TestDefaultConfigHasNoIgnoredSettingWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte(DefaultConfigTOML), 0644); err != nil {
		t.Fatal(err)
	}
	_, warnings, err := LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warnings {
		if strings.Contains(w, "not implemented yet") {
			t.Errorf("unexpected warning for the default config: %s", w)
		}
	}
}
