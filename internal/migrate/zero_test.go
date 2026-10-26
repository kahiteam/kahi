package migrate

import (
	"strings"
	"testing"
)

// supervisord's stopwaitsecs = 0 is written as 1 with a warning, so the
// converted config loads (#138).
func TestMigrateStopwaitsecsZero(t *testing.T) {
	input := "[program:web]\ncommand = /usr/bin/server\nstopwaitsecs = 0\nstartsecs = 0\n"
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.TOML, "stopwaitsecs = 1\n") {
		t.Errorf("TOML = %q, want stopwaitsecs = 1", result.TOML)
	}
	if !strings.Contains(result.TOML, "startsecs = 0\n") {
		t.Errorf("TOML = %q, want startsecs = 0 kept", result.TOML)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "programs.web: stopwaitsecs = 0 is not allowed; wrote 1") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want the stopwaitsecs warning", result.Warnings)
	}
	if len(result.ValidErrs) != 0 {
		t.Errorf("generated TOML does not validate: %v", result.ValidErrs)
	}
}
