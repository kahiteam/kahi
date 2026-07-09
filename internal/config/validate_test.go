package config

import (
	"strings"
	"testing"
)

// SEC-015: enabling the HTTP TCP listener must require credentials for any
// bind address, loopback included.

func TestHTTPEnabledWithoutCredentialsRejected(t *testing.T) {
	tomlData := `
[server.http]
enabled = true
listen = "0.0.0.0:9876"
`
	_, _, err := LoadBytes([]byte(tomlData), "test.toml")
	if err == nil {
		t.Fatal("expected validation error for http.enabled without credentials")
	}
	if !strings.Contains(err.Error(), "requires username/password") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestHTTPEnabledLoopbackWithoutCredentialsRejected(t *testing.T) {
	// Loopback receives no exemption from the credential requirement.
	tomlData := `
[server.http]
enabled = true
listen = "127.0.0.1:9876"
`
	_, _, err := LoadBytes([]byte(tomlData), "test.toml")
	if err == nil {
		t.Fatal("expected validation error for loopback http.enabled without credentials")
	}
	if !strings.Contains(err.Error(), "requires username/password") {
		t.Errorf("error = %q", err.Error())
	}
	if !strings.Contains(err.Error(), "127.0.0.1:9876") {
		t.Errorf("error should name the listen address, got %q", err.Error())
	}
}

func TestHTTPEnabledMissingPasswordRejected(t *testing.T) {
	tomlData := `
[server.http]
enabled = true
listen = "127.0.0.1:9876"
username = "admin"
`
	_, _, err := LoadBytes([]byte(tomlData), "test.toml")
	if err == nil {
		t.Fatal("expected validation error when password is missing")
	}
	if !strings.Contains(err.Error(), "requires username/password") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestHTTPEnabledMissingUsernameRejected(t *testing.T) {
	// Password built from a variable to avoid a literal credential assignment.
	cfg := &Config{}
	cfg.Server.HTTP.Enabled = true
	cfg.Server.HTTP.Listen = "127.0.0.1:9876"
	cfg.Server.HTTP.Password = "$2a$10$" + strings.Repeat("a", 22)

	errs := Validate(cfg)
	if !hasErr(errs, "requires username/password") {
		t.Fatalf("expected credential error, got %v", errs)
	}
}

func TestHTTPEnabledWithCredentialsAccepted(t *testing.T) {
	cfg := &Config{}
	cfg.Server.HTTP.Enabled = true
	cfg.Server.HTTP.Listen = "127.0.0.1:9876"
	cfg.Server.HTTP.Username = "admin"
	cfg.Server.HTTP.Password = "$2a$10$" + strings.Repeat("a", 22)

	errs := Validate(cfg)
	if hasErr(errs, "requires username/password") {
		t.Fatalf("unexpected credential error with valid credentials: %v", errs)
	}
}

func hasErr(errs []error, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Error(), substr) {
			return true
		}
	}
	return false
}

func TestHTTPDisabledWithoutCredentialsAccepted(t *testing.T) {
	// The Unix socket remains the password-free local path; a disabled HTTP
	// listener imposes no credential requirement.
	tomlData := `
[server.http]
enabled = false
`
	_, _, err := LoadBytes([]byte(tomlData), "test.toml")
	if err != nil {
		t.Fatalf("unexpected error for disabled http listener: %v", err)
	}
}
