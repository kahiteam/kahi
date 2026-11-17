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

	if !strings.Contains(result.TOML, `stopsignal = "SIGTERM"`) {
		t.Errorf("TOML = %s, want stopsignal = SIGTERM", result.TOML)
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

// A program followed by [include] must still produce TOML kahi can load
// (#111): before, include landed inside the last program table.
func TestMigrateIncludeAfterProgramIsValid(t *testing.T) {
	input := `[program:web]
command = /bin/sleep 300

[include]
files = conf.d/*.conf extra.conf
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.ValidErrs) > 0 {
		t.Fatalf("generated config is invalid: %v\n%s", result.ValidErrs, result.TOML)
	}
	if !strings.Contains(result.TOML, `# include = ["conf.d/*.conf", "extra.conf"]`) {
		t.Errorf("TOML = %s, want the split globs as a commented include", result.TOML)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "included INI files are not converted") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want a note that included INI files need migrating", result.Warnings)
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

	// The comment leaves the value and is kept at the end of the line
	// (FUNC-069, #158).
	if !strings.Contains(result.TOML, `command = "/usr/bin/python app.py" # start the web server`+"\n") {
		t.Errorf("inline comment not kept after the clean value:\n%s", result.TOML)
	}
}

// Comments travel with their item (FUNC-069, ADR-053).
func TestMigrateCommentPlacement(t *testing.T) {
	input := `; top of file
[supervisord] ; daemon
loglevel = debug ; chatty
identifier = h ; host id

; about web
[program:web]
; the binary
command = /bin/web
environment = A="1", ; continued
  B="2" ; dropped
; end of web

; group notes
[group:g]
programs = web
; trailing at end of file
`
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# top of file\n[supervisor] # daemon\n",
		"# (from supervisord) host id\n# UNSUPPORTED: identifier = h\n",
		`log_level = "debug" # chatty (renamed from "loglevel")` + "\n",
		"# about web\n[programs.web]\n# the binary\ncommand = \"/bin/web\"\n# end of web\n# continued\n[programs.web.environment]\n",
		"# group notes\n[groups.g]\nprograms = [\"web\"]\n# trailing at end of file\n",
	} {
		if !strings.Contains(result.TOML, want) {
			t.Errorf("missing %q in:\n%s", want, result.TOML)
		}
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "comment on continuation line 11 dropped") {
		t.Errorf("no continuation warning: %v", result.Warnings)
	}
	if strings.Contains(result.TOML, "dropped") {
		t.Error("continuation comment kept")
	}
	if len(result.ValidErrs) != 0 {
		t.Errorf("generated TOML invalid: %v", result.ValidErrs)
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

// The daemon's merged-config checks run too: process names and group
// references (#245).
func TestMigrateValidationRunsMergedChecks(t *testing.T) {
	for name, tc := range map[string]struct{ input, want string }{
		"process_name without process_num": {
			"[program:x]\ncommand=/bin/sleep 300\nnumprocs=2\nprocess_name=web\n",
			`programs.x: process_name "web" must contain %(process_num)d when numprocs > 1`,
		},
		"duplicate process name": {
			"[program:a]\ncommand=/bin/sleep 300\nprocess_name=same\n\n[program:b]\ncommand=/bin/sleep 300\nprocess_name=same\n",
			"duplicate process name: same (programs.a and programs.b)",
		},
		"group named like a program": {
			"[program:web]\ncommand=/bin/sleep 300\n\n[group:web]\nprograms=web\n",
			"groups.web: name collides with program web",
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := MigrateReader(strings.NewReader(tc.input), Options{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := strings.Join(result.ValidErrs, "\n"); !strings.Contains(got, tc.want) {
				t.Fatalf("validation errors %q lack %q", got, tc.want)
			}
		})
	}
}

// An event listener keeps its comments like a program (FUNC-069, FUNC-055).
func TestMigrateListenerComments(t *testing.T) {
	input := "[eventlistener:l] ; pool\n; the listener\ncommand=/bin/cat\nevents=PROCESS_STATE_EXITED ; exits only\nbuffer_size=5 ; small\nresult_handler=x:y ; custom\n"
	result, err := MigrateReader(strings.NewReader(input), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[eventlisteners.l] # pool\n",
		"buffer_size = 5 # small\n",
		"# the listener\ncommand = \"/bin/cat\"\n",
		"events = [\"PROCESS_STATE_EXITED\"] # exits only\n",
		"# (from supervisord) custom\n# UNSUPPORTED: result_handler = x:y",
	} {
		if !strings.Contains(result.TOML, want) {
			t.Errorf("missing %q in:\n%s", want, result.TOML)
		}
	}
}
