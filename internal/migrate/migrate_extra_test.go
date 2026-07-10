package migrate

import (
	"strings"
	"testing"
)

func TestMigrateWithUnixHTTPServer(t *testing.T) {
	input := `[unix_http_server]
file = /var/run/supervisor.sock
chmod = 0700
chown = nobody:nogroup

[supervisord]
logfile = /var/log/supervisord.log

[program:web]
command = /usr/bin/python -m http.server
`
	r := strings.NewReader(input)
	result, err := MigrateReader(r, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result.TOML, "[server.unix]") {
		t.Fatal("expected [server.unix] section")
	}
	if !strings.Contains(result.TOML, `file = "/var/run/supervisor.sock"`) {
		t.Fatalf("expected unix socket file in output, got:\n%s", result.TOML)
	}
	if !strings.Contains(result.TOML, `chmod = "0700"`) {
		t.Fatalf("expected chmod in output, got:\n%s", result.TOML)
	}
	// chown must never be emitted as active config: the control socket is
	// owner-only and Kahi rejects server.unix.chown, so it is reported as an
	// unsupported option instead, keeping migration output loadable.
	if strings.Contains(result.TOML, `chown = "nobody:nogroup"`) {
		t.Fatalf("chown must not be emitted as active config, got:\n%s", result.TOML)
	}
	if !strings.Contains(result.TOML, "# UNSUPPORTED: chown = nobody:nogroup") {
		t.Fatalf("expected chown reported as unsupported, got:\n%s", result.TOML)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, `server.unix: unsupported option "chown"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected chown warning, got: %v", result.Warnings)
	}
}

func TestMigrateWithInetHTTPServer(t *testing.T) {
	input := `[inet_http_server]
port = 127.0.0.1:9001
username = admin
password = secret123

[supervisord]
logfile = /var/log/supervisord.log

[program:api]
command = /usr/bin/api-server
`
	r := strings.NewReader(input)
	result, err := MigrateReader(r, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result.TOML, "[server.http]") {
		t.Fatal("expected [server.http] section")
	}
	if !strings.Contains(result.TOML, "enabled = true") {
		t.Fatal("expected enabled = true")
	}
	if !strings.Contains(result.TOML, `listen = "127.0.0.1:9001"`) {
		t.Fatalf("expected listen address, got:\n%s", result.TOML)
	}
	if !strings.Contains(result.TOML, `username = "admin"`) {
		t.Fatalf("expected username, got:\n%s", result.TOML)
	}
	if !strings.Contains(result.TOML, `password = "secret123"`) {
		t.Fatalf("expected password, got:\n%s", result.TOML)
	}
}

func TestMigrateWithEnvironment(t *testing.T) {
	input := `[supervisord]
logfile = /var/log/supervisord.log

[program:worker]
command = /usr/bin/worker
environment = APP_ENV="production",DB_HOST="localhost"
`
	r := strings.NewReader(input)
	result, err := MigrateReader(r, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result.TOML, "[programs.worker.environment]") {
		t.Fatalf("expected environment section, got:\n%s", result.TOML)
	}
	if !strings.Contains(result.TOML, `APP_ENV = "production"`) {
		t.Fatalf("expected APP_ENV, got:\n%s", result.TOML)
	}
	if !strings.Contains(result.TOML, `DB_HOST = "localhost"`) {
		t.Fatalf("expected DB_HOST, got:\n%s", result.TOML)
	}
}

func TestMigrateFileNotFound(t *testing.T) {
	_, err := Migrate("/nonexistent/supervisord.conf", Options{})
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("error = %q, want 'file not found'", err)
	}
}

func TestMigrateUnsupportedSection(t *testing.T) {
	input := `[supervisord]
logfile = /var/log/supervisord.log

[rpcinterface:supervisor]
supervisor.rpcinterface_factory = supervisor.rpcinterface:make_main_rpcinterface

[program:web]
command = /usr/bin/web
`
	r := strings.NewReader(input)
	result, err := MigrateReader(r, Options{})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "unsupported section") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unsupported section warning, got: %v", result.Warnings)
	}

	if !strings.Contains(result.TOML, "UNSUPPORTED SECTION") {
		t.Fatal("expected UNSUPPORTED SECTION comment in output")
	}
}

func TestMigrateWithBothServerSections(t *testing.T) {
	input := `[unix_http_server]
file = /tmp/supervisor.sock

[inet_http_server]
port = 0.0.0.0:9001

[supervisord]
logfile = /var/log/supervisord.log

[program:app]
command = /usr/bin/app
`
	r := strings.NewReader(input)
	result, err := MigrateReader(r, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result.TOML, "[server.unix]") {
		t.Fatal("expected [server.unix]")
	}
	if !strings.Contains(result.TOML, "[server.http]") {
		t.Fatal("expected [server.http]")
	}
}

func TestParseEnvironmentPairs(t *testing.T) {
	// Test through MigrateReader since parseEnvironment is unexported.
	input := `[supervisord]
logfile = /tmp/test.log

[program:test]
command = /bin/true
environment = SINGLE_VAR="value"
`
	r := strings.NewReader(input)
	result, err := MigrateReader(r, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result.TOML, `SINGLE_VAR = "value"`) {
		t.Fatalf("expected SINGLE_VAR, got:\n%s", result.TOML)
	}
}

func TestMigrateWithGroupSection(t *testing.T) {
	input := `[supervisord]
logfile = /tmp/test.log

[program:web1]
command = /usr/bin/web1

[program:web2]
command = /usr/bin/web2

[group:webapps]
programs = web1,web2
priority = 100
`
	r := strings.NewReader(input)
	result, err := MigrateReader(r, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result.TOML, "[groups.webapps]") {
		t.Fatalf("expected [groups.webapps], got:\n%s", result.TOML)
	}
}

func TestMigrateWithIncludeSection(t *testing.T) {
	input := `[supervisord]
logfile = /tmp/test.log

[include]
files = /etc/supervisor/conf.d/*.conf

[program:web]
command = /usr/bin/web
`
	r := strings.NewReader(input)
	result, err := MigrateReader(r, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result.TOML, "include") {
		t.Fatalf("expected include directive, got:\n%s", result.TOML)
	}
}
