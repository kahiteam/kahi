package migrate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateValidSupervisordConf(t *testing.T) {
	input := `[supervisord]
logfile = /var/log/supervisord.log
loglevel = info

[program:web]
command = /usr/bin/python app.py
autostart = true
autorestart = unexpected
startsecs = 10
startretries = 3
exitcodes = 0
stopsignal = TERM
stopwaitsecs = 10

[group:services]
programs = web,api
priority = 100
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TOML == "" {
		t.Fatal("TOML output is empty")
	}

	// Check that key sections are present.
	if !strings.Contains(result.TOML, "[supervisor]") {
		t.Error("missing [supervisor] section")
	}
	if !strings.Contains(result.TOML, "[programs.web]") {
		t.Error("missing [programs.web] section")
	}
	if !strings.Contains(result.TOML, "[groups.services]") {
		t.Error("missing [groups.services] section")
	}
	if !strings.Contains(result.TOML, `startsecs = 10`) {
		t.Error("missing startsecs = 10")
	}
	if !strings.Contains(result.TOML, `autorestart = "unexpected"`) {
		t.Error("missing autorestart = unexpected")
	}
}

func TestMigrateUnsupportedOptions(t *testing.T) {
	input := `[program:web]
command = /usr/bin/python app.py
serverurl = AUTO
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Warnings) == 0 {
		t.Error("expected warnings for unsupported option")
	}

	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "serverurl") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("warnings = %v, want mention of serverurl", result.Warnings)
	}

	if !strings.Contains(result.TOML, "# UNSUPPORTED") {
		t.Error("TOML should contain UNSUPPORTED comment")
	}
}

func TestMigrateNonexistentFile(t *testing.T) {
	_, err := Migrate("/nonexistent/supervisord.conf", Options{})
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Errorf("error = %q, want 'file not found'", err.Error())
	}
}

func TestMigrateInvalidINI(t *testing.T) {
	input := `this is not valid ini at all`
	_, err := MigrateReader(strings.NewReader(input), Options{})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse error") {
		t.Errorf("error = %q, want 'parse error'", err.Error())
	}
}

func TestMigrateOutputFileRefuse(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.toml")
	if err := os.WriteFile(existing, []byte("exists"), 0644); err != nil {
		t.Fatal(err)
	}

	result := &Result{TOML: "# test"}
	opts := Options{Output: existing}

	err := WriteResult(result, opts, nil)
	if err == nil {
		t.Fatal("expected error for existing output file")
	}
	if !strings.Contains(err.Error(), "output file exists") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestMigrateOutputFileForce(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.toml")
	if err := os.WriteFile(existing, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	result := &Result{TOML: "# new content"}
	opts := Options{Output: existing, Force: true}

	err := WriteResult(result, opts, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(existing)
	if string(data) != "# new content" {
		t.Errorf("file content = %q, want '# new content'", string(data))
	}
}

func TestMigrateDryRunPrintsToStdout(t *testing.T) {
	input := `[program:web]
command = /usr/bin/python app.py
`
	result, err := MigrateReader(strings.NewReader(input), Options{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	opts := Options{Output: "/should/not/be/written.toml", DryRun: true}
	if err := WriteResult(result, opts, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("dry run should print TOML to writer")
	}
	if !strings.Contains(buf.String(), "[programs.web]") {
		t.Error("dry run output should contain [programs.web]")
	}
}

func TestMigrateEnvironmentMapping(t *testing.T) {
	input := `[program:web]
command = /usr/bin/python app.py
environment = HOME="/app",PORT="8080"
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.TOML, `[programs.web.environment]`) {
		t.Error("missing environment sub-table")
	}
	if !strings.Contains(result.TOML, `HOME = "/app"`) {
		t.Error("missing HOME env var")
	}
	if !strings.Contains(result.TOML, `PORT = "8080"`) {
		t.Error("missing PORT env var")
	}
}

func TestMigrateSignalNormalization(t *testing.T) {
	input := `[program:web]
command = /usr/bin/python app.py
stopsignal = SIGTERM
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.TOML, `stopsignal = "TERM"`) {
		t.Errorf("TOML = %s, want stopsignal = TERM", result.TOML)
	}
}

func TestMigrateIncludeSection(t *testing.T) {
	input := `[include]
files = /etc/supervisor/conf.d/*.conf
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result.TOML, "include") {
		t.Error("missing include directive")
	}
}

func TestMigrateCommentsPreserved(t *testing.T) {
	input := `[program:web]
command = /usr/bin/python app.py ; start the web server
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The command value should have the comment stripped.
	if strings.Contains(result.TOML, "start the web server") {
		t.Error("inline comment should be stripped from value")
	}
	if !strings.Contains(result.TOML, `command = "/usr/bin/python app.py"`) {
		t.Errorf("TOML should contain clean command, got: %s", result.TOML)
	}
}

func TestMigrateGeneratedTOMLIsValid(t *testing.T) {
	input := `[program:web]
command = /usr/bin/python app.py
autostart = true
autorestart = unexpected
startsecs = 1
exitcodes = 0
stopsignal = TERM
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ValidErrs) > 0 {
		t.Errorf("validation errors: %v", result.ValidErrs)
	}
}

func TestMigrateValidationCatchesMissingCommand(t *testing.T) {
	input := `[program:web]
autostart = true
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ValidErrs) == 0 {
		t.Error("expected validation errors for missing command")
	}
}
