package config

import (
	"os"
	"testing"
)

func TestExpandHereVariable(t *testing.T) {
	cfg := &Config{
		Supervisor: SupervisorConfig{
			Directory: "%(here)s/data",
		},
		Programs: make(map[string]ProgramConfig),
	}

	err := ExpandVariables(cfg, "/etc/kahi/kahi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Supervisor.Directory != "/etc/kahi/data" {
		t.Fatalf("directory = %q, want /etc/kahi/data", cfg.Supervisor.Directory)
	}
}

func TestExpandEnvVar(t *testing.T) {
	t.Setenv("APP_BIN", "/usr/local/bin")

	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"server": {Command: "${APP_BIN}/server"},
		},
	}

	err := ExpandVariables(cfg, "/etc/kahi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Programs["server"].Command != "/usr/local/bin/server" {
		t.Fatalf("command = %q, want /usr/local/bin/server", cfg.Programs["server"].Command)
	}
}

func TestExpandProgramNameAndProcessNum(t *testing.T) {
	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"worker": {
				StdoutLogfile: "/var/log/%(program_name)s-%(process_num)d.log",
			},
		},
	}

	err := ExpandVariables(cfg, "/etc/kahi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Programs["worker"].StdoutLogfile != "/var/log/worker-0.log" {
		t.Fatalf("stdout_logfile = %q, want /var/log/worker-0.log", cfg.Programs["worker"].StdoutLogfile)
	}
}

func TestExpandUndefinedEnvVar(t *testing.T) {
	_ = os.Unsetenv("KAHI_TEST_UNDEF_VAR")

	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"test": {Command: "${KAHI_TEST_UNDEF_VAR}/bin"},
		},
	}

	err := ExpandVariables(cfg, "/etc/kahi.toml")
	if err == nil {
		t.Fatal("expected error for undefined env var")
	}
}

func TestExpandUnknownTemplateVar(t *testing.T) {
	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"test": {Command: "%(unknown_var)s/bin"},
		},
	}

	err := ExpandVariables(cfg, "/etc/kahi.toml")
	if err == nil {
		t.Fatal("expected error for unknown template var")
	}
}

func TestExpandNoRecursion(t *testing.T) {
	t.Setenv("KAHI_TEST_RECURSE", "%(here)s")

	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"test": {Command: "${KAHI_TEST_RECURSE}/bin"},
		},
	}

	err := ExpandVariables(cfg, "/etc/kahi.toml")
	if err != nil {
		t.Fatal(err)
	}

	// The result should be literal %(here)s/bin, not resolved further.
	if cfg.Programs["test"].Command != "%(here)s/bin" {
		t.Fatalf("command = %q, want literal %%(here)s/bin", cfg.Programs["test"].Command)
	}
}

func TestExpandEscapedPercent(t *testing.T) {
	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"test": {Command: "cmd --rate=50%%"},
		},
	}

	err := ExpandVariables(cfg, "/etc/kahi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Programs["test"].Command != "cmd --rate=50%" {
		t.Fatalf("command = %q, want 'cmd --rate=50%%'", cfg.Programs["test"].Command)
	}
}

func TestExpandEscapedDollar(t *testing.T) {
	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"test": {Command: "cmd --var=$$HOME"},
		},
	}

	err := ExpandVariables(cfg, "/etc/kahi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Programs["test"].Command != "cmd --var=$HOME" {
		t.Fatalf("command = %q, want 'cmd --var=$HOME'", cfg.Programs["test"].Command)
	}
}

func TestExpandGroupName(t *testing.T) {
	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"web": {
				Environment: map[string]string{
					"GROUP": "%(group_name)s",
				},
			},
		},
	}

	err := ExpandVariables(cfg, "/etc/kahi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Programs["web"].Environment["GROUP"] != "web" {
		t.Fatalf("GROUP = %q, want web", cfg.Programs["web"].Environment["GROUP"])
	}
}

func TestExpandMultipleVarsInSingleValue(t *testing.T) {
	t.Setenv("KAHI_TEST_HOST", "localhost")

	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"web": {
				Command: "${KAHI_TEST_HOST}/%(program_name)s",
			},
		},
	}

	err := ExpandVariables(cfg, "/etc/kahi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Programs["web"].Command != "localhost/web" {
		t.Fatalf("command = %q, want localhost/web", cfg.Programs["web"].Command)
	}
}

func TestExpandAtLoadTime(t *testing.T) {
	// Verify expansion happens during ExpandVariables call, not deferred.
	t.Setenv("KAHI_TEST_LOAD", "loaded")

	cfg := &Config{
		Programs: map[string]ProgramConfig{
			"test": {Command: "${KAHI_TEST_LOAD}/bin"},
		},
	}

	err := ExpandVariables(cfg, "/etc/kahi.toml")
	if err != nil {
		t.Fatal(err)
	}

	// Change env after expansion.
	t.Setenv("KAHI_TEST_LOAD", "changed")

	// Value should still be the original expansion.
	if cfg.Programs["test"].Command != "loaded/bin" {
		t.Fatalf("command = %q, want loaded/bin", cfg.Programs["test"].Command)
	}
}
