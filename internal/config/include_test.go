package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveIncludesGlob(t *testing.T) {
	dir := t.TempDir()

	// Main config.
	mainCfg := &Config{
		Include:  []string{filepath.Join(dir, "conf.d/*.toml")},
		Programs: make(map[string]ProgramConfig),
	}

	// Create conf.d directory with files.
	confDir := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}

	webCfg := `[programs.web]
command = "/usr/bin/web"
`
	apiCfg := `[programs.api]
command = "/usr/bin/api"
`
	if err := os.WriteFile(filepath.Join(confDir, "01-web.toml"), []byte(webCfg), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "02-api.toml"), []byte(apiCfg), 0644); err != nil {
		t.Fatal(err)
	}

	warnings, err := ResolveIncludes(mainCfg, dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(mainCfg.Programs) != 2 {
		t.Fatalf("expected 2 programs, got %d", len(mainCfg.Programs))
	}

	if _, ok := mainCfg.Programs["web"]; !ok {
		t.Fatal("missing program 'web'")
	}
	if _, ok := mainCfg.Programs["api"]; !ok {
		t.Fatal("missing program 'api'")
	}

	_ = warnings
}

func TestResolveIncludesNoMatches(t *testing.T) {
	dir := t.TempDir()

	cfg := &Config{
		Include:  []string{filepath.Join(dir, "nonexistent/*.toml")},
		Programs: make(map[string]ProgramConfig),
	}

	warnings, err := ResolveIncludes(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(warnings) == 0 {
		t.Fatal("expected warning for no-match pattern")
	}
}

func TestResolveIncludesRelativePath(t *testing.T) {
	dir := t.TempDir()

	// Create a relative include path.
	confDir := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}

	webCfg := `[programs.web]
command = "/usr/bin/web"
`
	if err := os.WriteFile(filepath.Join(confDir, "web.toml"), []byte(webCfg), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Include:  []string{"conf.d/*.toml"},
		Programs: make(map[string]ProgramConfig),
	}

	_, err := ResolveIncludes(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := cfg.Programs["web"]; !ok {
		t.Fatal("missing program 'web' from relative include")
	}
}

func TestResolveIncludesSyntaxError(t *testing.T) {
	dir := t.TempDir()
	confDir := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(confDir, "bad.toml"), []byte("[[invalid"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Include:  []string{filepath.Join(dir, "conf.d/*.toml")},
		Programs: make(map[string]ProgramConfig),
	}

	_, err := ResolveIncludes(cfg, dir)
	if err == nil {
		t.Fatal("expected error for syntax error in included file")
	}
}

func TestResolveIncludesDuplicateProgram(t *testing.T) {
	dir := t.TempDir()
	confDir := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}

	webCfg := `[programs.web]
command = "/usr/bin/web"
`
	if err := os.WriteFile(filepath.Join(confDir, "01.toml"), []byte(webCfg), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "02.toml"), []byte(webCfg), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Include:  []string{filepath.Join(dir, "conf.d/*.toml")},
		Programs: make(map[string]ProgramConfig),
	}

	_, err := ResolveIncludes(cfg, dir)
	if err == nil {
		t.Fatal("expected error for duplicate program name")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %q, want duplicate program error", err.Error())
	}
}

func TestResolveIncludesClearsIncludeField(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Include:  []string{filepath.Join(dir, "nonexistent/*.toml")},
		Programs: make(map[string]ProgramConfig),
	}

	_, _ = ResolveIncludes(cfg, dir)

	if cfg.Include != nil {
		t.Fatal("include field should be cleared after resolution")
	}
}

func TestValidateGroupReferencesEmpty(t *testing.T) {
	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"web": {Command: "/usr/bin/web"},
		},
		Groups: map[string]GroupConfig{
			"empty": {Programs: []string{}},
		},
	}

	errs := validateGroupReferences(cfg)
	if len(errs) == 0 {
		t.Fatal("expected error for empty group")
	}
}

func TestValidateGroupReferencesNonexistent(t *testing.T) {
	cfg := &Config{
		Programs: map[string]ProgramConfig{},
		Groups: map[string]GroupConfig{
			"mygroup": {Programs: []string{"nonexistent"}},
		},
	}

	errs := validateGroupReferences(cfg)
	if len(errs) == 0 {
		t.Fatal("expected error for nonexistent program reference")
	}
}

func TestValidateGroupReferencesMultipleGroups(t *testing.T) {
	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"web": {Command: "/usr/bin/web"},
		},
		Groups: map[string]GroupConfig{
			"group1": {Programs: []string{"web"}},
			"group2": {Programs: []string{"web"}},
		},
	}

	errs := validateGroupReferences(cfg)
	if len(errs) == 0 {
		t.Fatal("expected error for program in multiple groups")
	}
}

func TestLoadWithIncludesWarnsStopwaitsecsOverShutdownTimeout(t *testing.T) {
	dir := t.TempDir()
	confDir := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}

	mainCfg := `include = ["` + filepath.Join(confDir, "*.toml") + `"]

[supervisor]
shutdown_timeout = 30

[programs.web]
command = "/usr/bin/web"
stopwaitsecs = 30
`
	// The slow program comes from an include, so the check must run after
	// includes are merged.
	dbCfg := `[programs.db]
command = "/usr/bin/db"
stopwaitsecs = 120
`
	mainPath := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(mainPath, []byte(mainCfg), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "db.toml"), []byte(dbCfg), 0644); err != nil {
		t.Fatal(err)
	}

	_, warnings, err := LoadWithIncludes(mainPath)
	if err != nil {
		t.Fatal(err)
	}

	var found []string
	for _, w := range warnings {
		if strings.Contains(w, "stopwaitsecs") {
			found = append(found, w)
		}
	}
	if len(found) != 1 {
		t.Fatalf("stopwaitsecs warnings = %q, want exactly one, for db", found)
	}
	for _, want := range []string{`"db"`, "120", "30"} {
		if !strings.Contains(found[0], want) {
			t.Fatalf("warning %q does not mention %s", found[0], want)
		}
	}
}
