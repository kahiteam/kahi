package migrate

import (
	"strings"
	"testing"
)

// Keys the daemon parses but ignores are flagged by migrate instead of being
// written as settings that would have no effect (#140).
func TestMigrateFlagsKeysTheDaemonIgnores(t *testing.T) {
	input := `[supervisord]
minfds = 4096
minprocs = 500
nocleanup = true
directory = /srv
identifier = host1

[program:web]
command = /usr/bin/server
stdout_capture_maxbytes = 1MB
stderr_capture_maxbytes = 1MB
stdout_syslog = true
stderr_syslog = true
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warnings := strings.Join(result.Warnings, "\n")
	for _, key := range []string{
		"nocleanup", "identifier",
		"stdout_capture_maxbytes", "stderr_capture_maxbytes", "stdout_syslog", "stderr_syslog",
	} {
		if !strings.Contains(warnings, `"`+key+`"`) {
			t.Errorf("no unsupported-option warning for %s; warnings:\n%s", key, warnings)
		}
		if !strings.Contains(result.TOML, "# UNSUPPORTED: "+key+" = ") {
			t.Errorf("TOML does not comment out %s:\n%s", key, result.TOML)
		}
	}
	if len(result.ValidErrs) != 0 {
		t.Errorf("generated TOML does not validate: %v", result.ValidErrs)
	}
	// FUNC-086: kahi has its own *_capture_maxbytes (a buffer size); the
	// supervisord key means something else, so it is not carried over.
	for _, key := range []string{"stdout_capture_maxbytes", "stderr_capture_maxbytes"} {
		want := "# UNSUPPORTED: " + key + " = 1MB (supervisord capture mode for PROCESS_COMMUNICATION events, not a buffer size)"
		if !strings.Contains(result.TOML, want) {
			t.Errorf("TOML lacks %q:\n%s", want, result.TOML)
		}
	}
	if !strings.Contains(result.TOML, "# UNSUPPORTED: nocleanup = true (has no effect: kahi does not auto-name child logs)") {
		t.Errorf("nocleanup comment lacks the FUNC-023 note:\n%s", result.TOML)
	}
}

// The daemon applies these since v0.3.0 (#113, #160), so migrate maps them.
func TestMigrateMapsDaemonSettings(t *testing.T) {
	input := `[supervisord]
minfds = 4096
minprocs = 500
directory = /srv
umask = 022

[program:web]
command = /usr/bin/server
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"minfds = 4096", "minprocs = 500", `directory = "/srv"`, `umask = "022"`} {
		if !strings.Contains(result.TOML, want) {
			t.Errorf("TOML lacks %q:\n%s", want, result.TOML)
		}
	}
	if len(result.ValidErrs) != 0 {
		t.Errorf("generated TOML does not validate: %v", result.ValidErrs)
	}
}
