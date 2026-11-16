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
identifier = "web-host"
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
		"supervisor.identifier",
		"programs.web.stdout_syslog",
		"programs.web.stderr_syslog",
	} {
		if !strings.Contains(joined, want+" ") && !strings.Contains(joined, want+":") {
			t.Errorf("no ignored-setting warning for %s; warnings:\n%s", want, joined)
		}
	}
	// Implemented in v0.3.0 (#113): no ignored warning.
	for _, gone := range []string{
		"supervisor.directory", "supervisor.minfds", "supervisor.minprocs",
		"programs.web.stdout_capture_maxbytes", "programs.web.stderr_capture_maxbytes",
	} {
		if strings.Contains(joined, gone) {
			t.Errorf("warning for %s, which is implemented; warnings:\n%s", gone, joined)
		}
	}
	// FUNC-023 is withdrawn: nocleanup has no effect.
	if !strings.Contains(joined, "supervisor.nocleanup has no effect: kahi does not auto-name child logs") {
		t.Errorf("no nocleanup warning; warnings:\n%s", joined)
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

// stderr_events_enabled has no effect with redirect_stderr (FUNC-051).
func TestRedirectStderrEventsWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte(`[programs.p]
command = "/bin/p"
redirect_stderr = true
stdout_events_enabled = true
stderr_events_enabled = true

[programs.q]
command = "/bin/q"
stderr_events_enabled = true
`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, warnings, err := LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Programs["p"].StdoutEventsEnabled || !cfg.Programs["q"].StderrEventsEnabled {
		t.Fatalf("flags not parsed: %+v", cfg.Programs)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "programs.p.stderr_events_enabled has no effect with redirect_stderr") {
		t.Errorf("no redirect warning; warnings:\n%s", joined)
	}
	if strings.Contains(joined, "programs.q.stderr_events_enabled") {
		t.Errorf("warning for q, which does not redirect; warnings:\n%s", joined)
	}
}

func TestValidUmask(t *testing.T) {
	for in, want := range map[string]bool{"0022": true, "022": true, "7": true, "0777": true, "0999": false, "abc": false, "01777": false, "": false, "00000": false} {
		if got := ValidUmask(in); got != want {
			t.Errorf("ValidUmask(%q) = %v, want %v", in, got, want)
		}
	}
}

// An invalid umask in [supervisor] or a program is a load error (FUNC-047).
func TestInvalidUmaskRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte(`[supervisor]
umask = "0999"

[programs.web]
command = "/bin/web"
umask = "abc"
`), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadWithIncludes(path)
	if err == nil || !strings.Contains(err.Error(), "supervisor.umask: invalid umask: 0999") || !strings.Contains(err.Error(), "programs.web.umask: invalid umask: abc") {
		t.Fatalf("err = %v, want both invalid umask errors", err)
	}
}

// minfds/minprocs present in the file are explicit (FUNC-068).
func TestExplicitLimitKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte("[supervisor]\nminfds = 1024\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Supervisor.MinfdsSet || cfg.Supervisor.MinprocsSet || cfg.Supervisor.Minprocs != 200 {
		t.Fatalf("MinfdsSet %v MinprocsSet %v Minprocs %d, want true false 200", cfg.Supervisor.MinfdsSet, cfg.Supervisor.MinprocsSet, cfg.Supervisor.Minprocs)
	}
}
