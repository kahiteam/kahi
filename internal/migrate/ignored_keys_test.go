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
		"minfds", "minprocs", "nocleanup", "directory", "identifier",
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
}
