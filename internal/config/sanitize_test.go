package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// SEC-014: the config API must not expose secrets. Sanitized masks environment
// and webhook header values, strips webhook URL userinfo, and json:"-" keeps
// HTTP credentials from ever serializing.

func TestSanitizedMasksProgramEnvironmentValues(t *testing.T) {
	c := &Config{
		Programs: map[string]ProgramConfig{
			"web": {
				Command:  "/usr/bin/web",
				Numprocs: 2,
				Environment: map[string]string{
					"DB_PASSWORD": "s3cr3t",
					"API_KEY":     "ak-live-123",
				},
			},
		},
	}

	got := Sanitized(c)

	env := got.Programs["web"].Environment
	if env["DB_PASSWORD"] != RedactMask || env["API_KEY"] != RedactMask {
		t.Fatalf("environment values not masked: %v", env)
	}
	if _, ok := env["DB_PASSWORD"]; !ok {
		t.Fatal("environment key DB_PASSWORD should be preserved")
	}
	if got.Programs["web"].Command != "/usr/bin/web" || got.Programs["web"].Numprocs != 2 {
		t.Fatal("non-secret program fields must pass through unchanged")
	}

	// The original config must not be mutated.
	if c.Programs["web"].Environment["DB_PASSWORD"] != "s3cr3t" {
		t.Fatal("Sanitized mutated the source config")
	}
}

func TestSanitizedMasksWebhookHeadersAndURLUserinfo(t *testing.T) {
	c := &Config{
		Webhooks: map[string]WebhookConfig{
			"alerts": {
				URL: "https://user:hunter2@hooks.example.com/notify",
				Headers: map[string]string{
					"Authorization": "Bearer tok-abc",
				},
			},
		},
	}

	got := Sanitized(c)

	wh := got.Webhooks["alerts"]
	if wh.Headers["Authorization"] != RedactMask {
		t.Fatalf("webhook header not masked: %v", wh.Headers)
	}
	if strings.Contains(wh.URL, "hunter2") || strings.Contains(wh.URL, "user:") {
		t.Fatalf("webhook URL userinfo not stripped: %s", wh.URL)
	}
	if !strings.Contains(wh.URL, "hooks.example.com/notify") {
		t.Fatalf("webhook URL host/path lost during sanitization: %s", wh.URL)
	}
}

func TestSanitizedHandlesNilAndEmptyMaps(t *testing.T) {
	if Sanitized(nil) != nil {
		t.Fatal("Sanitized(nil) should return nil")
	}

	c := &Config{
		Programs: map[string]ProgramConfig{"p": {Command: "/bin/true"}},
		Webhooks: map[string]WebhookConfig{"w": {URL: "https://example.com"}},
	}
	got := Sanitized(c)
	if got.Programs["p"].Environment != nil {
		t.Fatal("nil environment should remain nil")
	}
	if got.Webhooks["w"].URL != "https://example.com" {
		t.Fatalf("URL without userinfo should be unchanged: %s", got.Webhooks["w"].URL)
	}
}

func TestHTTPCredentialsNeverSerialize(t *testing.T) {
	// Seeded via variables so the fixtures do not resemble committed secrets.
	httpUser := "admin"
	httpPass := "topsecret"
	c := &Config{
		Server: ServerConfig{
			HTTP: HTTPServerConfig{
				Enabled:  true,
				Listen:   "127.0.0.1:9876",
				Username: httpUser,
				Password: httpPass,
			},
		},
	}

	out, err := json.Marshal(Sanitized(c))
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	if strings.Contains(body, httpPass) || strings.Contains(body, httpUser) {
		t.Fatalf("HTTP credentials leaked into JSON: %s", body)
	}
}
