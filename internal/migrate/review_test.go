package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

// loadMigrated migrates in and loads the result as the daemon does,
// expansion included.
func loadMigrated(t *testing.T, in string) (*config.Config, *Result) {
	t.Helper()
	r, err := MigrateReader(strings.NewReader(in), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ValidErrs) > 0 {
		t.Fatalf("validation: %v\n%s", r.ValidErrs, r.TOML)
	}
	p := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(p, []byte(r.TOML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadWithIncludes(p)
	if err != nil {
		t.Fatalf("daemon load: %v\n%s", err, r.TOML)
	}
	return cfg, r
}

// Section names that are not bare TOML keys are quoted, so a dot does not
// nest tables and a program is never lost.
func TestMigrateQuotesSectionNames(t *testing.T) {
	cfg, _ := loadMigrated(t, "[program:api]\ncommand=/bin/a\n[program:api.worker]\ncommand=/bin/w\n[program:my app]\ncommand=/bin/s\n[group:g.x]\nprograms=api.worker\n")
	for _, name := range []string{"api", "api.worker", "my app"} {
		if _, ok := cfg.Programs[name]; !ok {
			t.Errorf("program %q lost: %v", name, cfg.Programs)
		}
	}
	if g, ok := cfg.Groups["g.x"]; !ok || len(g.Programs) != 1 || g.Programs[0] != "api.worker" {
		t.Errorf("group g.x: %+v", cfg.Groups)
	}
}

// Commas and = inside quotes belong to an environment value.
func TestMigrateEnvironmentQuotedCommas(t *testing.T) {
	cfg, _ := loadMigrated(t, "[program:a]\ncommand=/bin/true\nenvironment=JAVA_OPTS=\"-Xms1g,-Xmx2g\",B=z,C='x=y'\n")
	env := cfg.Programs["a"].Environment
	if env["JAVA_OPTS"] != "-Xms1g,-Xmx2g" || env["B"] != "z" || env["C"] != "x=y" {
		t.Errorf("environment %v", env)
	}
}

// supervisord takes $ literally: the migrated config keeps $$ and ${X} as
// the shell would see them, and %(ENV_X)s still becomes Kahi's ${X}.
func TestMigrateEscapesDollar(t *testing.T) {
	t.Setenv("ZZ_MIGRATE_HOME", "/home/x")
	cfg, _ := loadMigrated(t, "[program:a]\ncommand=/bin/sh -c 'echo $$ ${ZZ_UNSET}'\ndirectory=%(ENV_ZZ_MIGRATE_HOME)s\nenvironment=P=\"a$b\"\n")
	p := cfg.Programs["a"]
	if p.Command != "/bin/sh -c 'echo $$ ${ZZ_UNSET}'" {
		t.Errorf("command %q", p.Command)
	}
	if p.Directory != "/home/x" {
		t.Errorf("directory %q", p.Directory)
	}
	if p.Environment["P"] != "a$b" {
		t.Errorf("environment P %q", p.Environment["P"])
	}
}

// Control characters and invalid UTF-8 are written as valid TOML.
func TestMigrateTOMLStrings(t *testing.T) {
	cfg, _ := loadMigrated(t, "[program:a]\ncommand=/bin/echo \x07bell \xff\n")
	if c := cfg.Programs["a"].Command; c != "/bin/echo \x07bell �" {
		t.Errorf("command %q", c)
	}
}

// NONE and AUTO log values are omitted with a warning.
func TestMigrateLogfileNoneAuto(t *testing.T) {
	cfg, r := loadMigrated(t, "[program:a]\ncommand=/bin/a\nstdout_logfile=NONE\nstderr_logfile=auto\n")
	p := cfg.Programs["a"]
	if p.StdoutLogfile != "" || p.StderrLogfile != "" {
		t.Errorf("log files %q %q", p.StdoutLogfile, p.StderrLogfile)
	}
	w := strings.Join(r.Warnings, "\n")
	if !strings.Contains(w, "programs.a: stdout_logfile = NONE omitted") || !strings.Contains(w, "programs.a: stderr_logfile = AUTO omitted") {
		t.Errorf("warnings %q", w)
	}
}
