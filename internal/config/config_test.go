package config

import (
	"strings"
	"testing"
)

func TestParseValidConfig(t *testing.T) {
	tomlData := `
[supervisor]
log_level = "debug"
log_format = "text"
minfds = 4096

[programs.web]
command = "/usr/bin/python3 -m http.server"
numprocs = 2
priority = 100
autostart = true
autorestart = "unexpected"
startsecs = 5
startretries = 5
exitcodes = [0, 2]
stopsignal = "TERM"
stopwaitsecs = 15
description = "web server"
`
	cfg, warnings, err := LoadBytes([]byte(tomlData), "test.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	if cfg.Supervisor.LogLevel != "debug" {
		t.Errorf("log_level = %q, want debug", cfg.Supervisor.LogLevel)
	}
	if cfg.Supervisor.LogFormat != "text" {
		t.Errorf("log_format = %q, want text", cfg.Supervisor.LogFormat)
	}
	if cfg.Supervisor.Minfds != 4096 {
		t.Errorf("minfds = %d, want 4096", cfg.Supervisor.Minfds)
	}

	web, ok := cfg.Programs["web"]
	if !ok {
		t.Fatal("missing programs.web")
	}
	if web.Command != "/usr/bin/python3 -m http.server" {
		t.Errorf("command = %q", web.Command)
	}
	if web.Numprocs != 2 {
		t.Errorf("numprocs = %d, want 2", web.Numprocs)
	}
	if web.Priority != 100 {
		t.Errorf("priority = %d, want 100", web.Priority)
	}
}

func TestEmptyConfigGetsDefaults(t *testing.T) {
	cfg, _, err := LoadBytes([]byte(""), "empty.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Supervisor.LogLevel != "info" {
		t.Errorf("default log_level = %q, want info", cfg.Supervisor.LogLevel)
	}
	if cfg.Supervisor.LogFormat != "json" {
		t.Errorf("default log_format = %q, want json", cfg.Supervisor.LogFormat)
	}
	if cfg.Supervisor.Minfds != 1024 {
		t.Errorf("default minfds = %d, want 1024", cfg.Supervisor.Minfds)
	}
	if cfg.Supervisor.Minprocs != 200 {
		t.Errorf("default minprocs = %d, want 200", cfg.Supervisor.Minprocs)
	}
	if cfg.Supervisor.ShutdownTimeout != 30 {
		t.Errorf("default shutdown_timeout = %d, want 30", cfg.Supervisor.ShutdownTimeout)
	}
	if cfg.Server.Unix.File != "/var/run/kahi.sock" {
		t.Errorf("default unix file = %q", cfg.Server.Unix.File)
	}
}

func TestMissingCommandProducesError(t *testing.T) {
	tomlData := `
[programs.web]
numprocs = 1
`
	_, _, err := LoadBytes([]byte(tomlData), "test.toml")
	if err == nil {
		t.Fatal("expected validation error for missing command")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("error = %q, want 'command is required'", err.Error())
	}
}

func TestOutOfRangePriorityProducesError(t *testing.T) {
	tomlData := `
[programs.web]
command = "/bin/true"
priority = 1500
`
	_, _, err := LoadBytes([]byte(tomlData), "test.toml")
	if err == nil {
		t.Fatal("expected validation error for out-of-range priority")
	}
	if !strings.Contains(err.Error(), "priority must be between 0 and 999") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestInvalidAutorestartProducesError(t *testing.T) {
	tomlData := `
[programs.web]
command = "/bin/true"
autorestart = "always"
`
	_, _, err := LoadBytes([]byte(tomlData), "test.toml")
	if err == nil {
		t.Fatal("expected validation error for invalid autorestart")
	}
	if !strings.Contains(err.Error(), "autorestart must be true, false, or unexpected") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestUnknownFieldsProduceWarnings(t *testing.T) {
	tomlData := `
[supervisor]
log_level = "info"
unknown_field = "value"
`
	cfg, warnings, err := LoadBytes([]byte(tomlData), "test.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("config is nil")
	}
	if len(warnings) == 0 {
		t.Fatal("expected warnings for unknown field")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "unknown_field") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("warnings = %v, want mention of unknown_field", warnings)
	}
}

func TestKillasgroupValidation(t *testing.T) {
	tomlData := `
[programs.web]
command = "/bin/true"
stopasgroup = true
killasgroup = false
`
	_, _, err := LoadBytes([]byte(tomlData), "test.toml")
	if err == nil {
		t.Fatal("expected validation error for killasgroup=false with stopasgroup=true")
	}
	if !strings.Contains(err.Error(), "killasgroup cannot be false when stopasgroup is true") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestSupervisorSectionParsing(t *testing.T) {
	tomlData := `
[supervisor]
logfile = "/var/log/kahi.log"
identifier = "kahi-prod"
shutdown_timeout = 60
nocleanup = true
`
	cfg, _, err := LoadBytes([]byte(tomlData), "test.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Supervisor.Logfile != "/var/log/kahi.log" {
		t.Errorf("logfile = %q", cfg.Supervisor.Logfile)
	}
	if cfg.Supervisor.Identifier != "kahi-prod" {
		t.Errorf("identifier = %q", cfg.Supervisor.Identifier)
	}
	if cfg.Supervisor.ShutdownTimeout != 60 {
		t.Errorf("shutdown_timeout = %d, want 60", cfg.Supervisor.ShutdownTimeout)
	}
	if !cfg.Supervisor.Nocleanup {
		t.Error("nocleanup should be true")
	}
}
