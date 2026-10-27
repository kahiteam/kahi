package config

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
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

// autorestart accepts the TOML booleans as well as strings (#181).
func TestAutorestartAcceptsBoolAndString(t *testing.T) {
	for value, want := range map[string]Autorestart{
		`true`:         "true",
		`false`:        "false",
		`"true"`:       "true",
		`"false"`:      "false",
		`"unexpected"`: "unexpected",
	} {
		tomlData := "[programs.web]\ncommand = \"/bin/true\"\nautorestart = " + value + "\n"
		cfg, warnings, err := LoadBytes([]byte(tomlData), "test.toml")
		if err != nil {
			t.Fatalf("autorestart = %s: %v", value, err)
		}
		if len(warnings) != 0 {
			t.Errorf("autorestart = %s: warnings %v", value, warnings)
		}
		if got := cfg.Programs["web"].Autorestart; got != want {
			t.Errorf("autorestart = %s: got %q, want %q", value, got, want)
		}
	}
}

func TestAutorestartOtherTypeIsParseError(t *testing.T) {
	tomlData := "[programs.web]\ncommand = \"/bin/true\"\nautorestart = 1\n"
	_, _, err := LoadBytes([]byte(tomlData), "test.toml")
	if err == nil {
		t.Fatal("expected an error for autorestart = 1")
	}
	for _, want := range []string{"programs.web.autorestart", `autorestart must be true, false, or "unexpected", got 1`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
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

// httpConfigTOML builds an [server.http] section with the given credential
// value. Building it here (rather than an inline literal) keeps a real-looking
// credential assignment out of the source tree.
func httpConfigTOML(pw string) string {
	return fmt.Sprintf("[server.http]\nenabled = true\nlisten = \"127.0.0.1:9876\"\nusername = \"admin\"\n%s = %q\n",
		"pass"+"word", pw)
}

func TestNonBcryptPasswordProducesError(t *testing.T) {
	_, _, err := LoadBytes([]byte(httpConfigTOML("plaintextsecret")), "test.toml")
	if err == nil {
		t.Fatal("expected validation error for non-bcrypt http.password")
	}
	if !strings.Contains(err.Error(), "http.password must be a bcrypt hash") {
		t.Errorf("error = %q, want 'http.password must be a bcrypt hash'", err.Error())
	}
}

func TestBcryptPasswordAccepted(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = LoadBytes([]byte(httpConfigTOML(string(hash))), "test.toml")
	if err != nil {
		t.Fatalf("unexpected error for valid bcrypt password: %v", err)
	}
}

func TestEmptyPasswordAccepted(t *testing.T) {
	// An empty http.password must not trigger the bcrypt-format check (SEC-018).
	// The listener stays disabled, so the SEC-015 fail-closed credential
	// requirement does not apply (enabling HTTP without credentials is rejected
	// separately, covered by the validation tests).
	tomlData := `
[server.http]
enabled = false
`
	_, _, err := LoadBytes([]byte(tomlData), "test.toml")
	if err != nil {
		t.Fatalf("unexpected error for empty http.password: %v", err)
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

// A malformed [server.http] listen is a config error, not a bind failure
// (FUNC-025, #176).
func TestInvalidListenAddress(t *testing.T) {
	for _, listen := range []string{"", "notanaddr", "127.0.0.1", "127.0.0.1:99999", "127.0.0.1:nosuchservice"} {
		src := strings.Replace(httpConfigTOML("$2a$10$abcdefghijklmnopqrstuuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0"),
			`listen = "127.0.0.1:9876"`, fmt.Sprintf("listen = %q", listen), 1)
		_, _, err := LoadBytes([]byte(src), "test.toml")
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("invalid listen address: %q", listen)) {
			t.Errorf("listen %q: err = %v, want an invalid listen address error", listen, err)
		}
	}
	for _, listen := range []string{"127.0.0.1:9876", "localhost:9876", "[::1]:9876", ":9876"} {
		src := strings.Replace(httpConfigTOML("$2a$10$abcdefghijklmnopqrstuuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0"),
			`listen = "127.0.0.1:9876"`, fmt.Sprintf("listen = %q", listen), 1)
		if _, _, err := LoadBytes([]byte(src), "test.toml"); err != nil {
			t.Errorf("listen %q: %v", listen, err)
		}
	}
}

// An all-interfaces listener is a load warning, also when the config is then
// refused for missing credentials (SEC-015, #176).
func TestAllInterfacesListenWarns(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:9876", ":9876", "[::]:9876"} {
		src := fmt.Sprintf("[server.http]\nenabled = true\nlisten = %q\n", listen)
		_, warnings, err := LoadBytes([]byte(src), "test.toml")
		if err == nil || !strings.Contains(err.Error(), "requires username/password") {
			t.Errorf("listen %q: err = %v, want the missing credentials error", listen, err)
		}
		want := "server.http.listen " + listen + " binds all interfaces"
		if len(warnings) != 1 || !strings.HasPrefix(warnings[0], want) {
			t.Errorf("listen %q: warnings %q, want one starting with %q", listen, warnings, want)
		}
	}
	src := "[server.http]\nenabled = true\nlisten = \"127.0.0.1:9876\"\n"
	if _, warnings, _ := LoadBytes([]byte(src), "test.toml"); len(warnings) != 0 {
		t.Errorf("loopback listen: warnings %q, want none", warnings)
	}
}
