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

// The multiple-groups error names the groups in sorted order, whatever the
// map order.
func TestValidateGroupReferencesMultipleGroupsSorted(t *testing.T) {
	cfg := &Config{
		Programs: map[string]ProgramConfig{"web": {Command: "/usr/bin/web"}},
		Groups: map[string]GroupConfig{
			"zeta":  {Programs: []string{"web"}},
			"alpha": {Programs: []string{"web"}},
		},
	}
	for i := 0; i < 20; i++ {
		errs := validateGroupReferences(cfg)
		if len(errs) != 1 || errs[0].Error() != `program "web" is in multiple groups: alpha and zeta` {
			t.Fatalf("errs = %v", errs)
		}
	}
}

// A [groups] table named like a program would replace the program's own
// group; it is refused (#214).
func TestValidateGroupReferencesNameCollision(t *testing.T) {
	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"web": {Command: "/usr/bin/web"},
			"api": {Command: "/usr/bin/api"},
		},
		Groups: map[string]GroupConfig{
			"web":      {Programs: []string{"api"}},
			"services": {Programs: []string{"web"}},
		},
	}
	errs := validateGroupReferences(cfg)
	if len(errs) != 1 || errs[0].Error() != "groups.web: name collides with program web" {
		t.Fatalf("errs = %v, want only the collision", errs)
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

	// web and db share the default priority, so they form one level: the
	// largest stopwaitsecs, 120, is over the 30 s limit.
	var found []string
	for _, w := range warnings {
		if strings.Contains(w, "ordered shutdown") {
			found = append(found, w)
		}
	}
	want := "ordered shutdown can take up to 120s (largest stopwaitsecs per priority level, summed), more than supervisor.shutdown_timeout (30s); processes still running then are SIGKILLed"
	if len(found) != 1 || found[0] != want {
		t.Fatalf("shutdown warnings = %q, want exactly %q", found, want)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// Variables in included files are expanded, with %(here)s naming the
// included file's own directory (#109).
func TestLoadWithIncludesExpandsIncludedFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KAHI_INCLUDE_TEST", "from-env")
	writeFile(t, filepath.Join(dir, "kahi.toml"), `include = ["conf.d/*.toml"]`+"\n")
	writeFile(t, filepath.Join(dir, "conf.d", "web.toml"), `[programs.web]
command = "%(here)s/run %(program_name)s ${KAHI_INCLUDE_TEST}"
`)

	cfg, _, err := LoadWithIncludes(filepath.Join(dir, "kahi.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "conf.d") + "/run web from-env"
	if got := cfg.Programs["web"].Command; got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

// An included file's own include is processed, relative to that file.
func TestLoadWithIncludesNested(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "kahi.toml"), `include = ["conf.d/*.toml"]`+"\n")
	writeFile(t, filepath.Join(dir, "conf.d", "a.toml"), `include = ["more/*.toml"]

[programs.a]
command = "/bin/a"
`)
	writeFile(t, filepath.Join(dir, "conf.d", "more", "b.toml"), `[programs.b]
command = "/bin/b"
`)

	cfg, _, err := LoadWithIncludes(filepath.Join(dir, "kahi.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if _, ok := cfg.Programs[name]; !ok {
			t.Fatalf("program %q from a nested include is missing", name)
		}
	}
}

// A file that includes the main config back is a cycle, not an endless loop.
func TestLoadWithIncludesCycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "kahi.toml"), `include = ["conf.d/*.toml"]`+"\n")
	writeFile(t, filepath.Join(dir, "conf.d", "loop.toml"), `include = ["../kahi.toml"]`+"\n")

	_, _, err := LoadWithIncludes(filepath.Join(dir, "kahi.toml"))
	if err == nil || !strings.Contains(err.Error(), "circular include") {
		t.Fatalf("err = %v, want a circular include error", err)
	}
}

// Levels add up: two levels of 20 s exceed 30 s, one level of 20 s does
// not; a [groups] member counts at its table's priority (ADR-031).
func TestShutdownWarningsSumLevels(t *testing.T) {
	cfg := func(dbPriority int, groups map[string]GroupConfig) *Config {
		return &Config{
			Supervisor: SupervisorConfig{ShutdownTimeout: 30},
			Programs: map[string]ProgramConfig{
				"web": {Priority: 100, Stopwaitsecs: 20},
				"db":  {Priority: dbPriority, Stopwaitsecs: 20},
			},
			Groups: groups,
		}
	}
	if w := shutdownWarnings(cfg(200, nil)); len(w) != 1 || !strings.Contains(w[0], "up to 40s") {
		t.Errorf("two levels: %q, want a warning for 40s", w)
	}
	if w := shutdownWarnings(cfg(100, nil)); len(w) != 0 {
		t.Errorf("one level: %q, want none", w)
	}
	// db in a table of priority 100 shares web's level key only if its own
	// priority matches too; with own 200 it is the level (100, 200).
	g := map[string]GroupConfig{"t": {Programs: []string{"db"}, Priority: 100}}
	if gk, own := OrderKey(cfg(200, g), "db"); gk != 100 || own != 200 {
		t.Errorf("OrderKey(db) = %d, %d, want the table priority 100 and own 200", gk, own)
	}
}
