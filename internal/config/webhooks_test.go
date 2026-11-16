package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadWebhooks writes body as kahi.toml and loads it with includes, so
// validation, expansion and warnings all run (FUNC-056..059).
func loadWebhooks(t *testing.T, body string) (*Config, []string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadWithIncludes(path)
}

func TestWebhookDefaults(t *testing.T) {
	cfg, warnings, err := loadWebhooks(t, `[webhooks.t]
url = "https://hooks.example.com/x"
events = ["PROCESS_STATE_FATAL"]
`)
	if err != nil {
		t.Fatal(err)
	}
	w := cfg.Webhooks["t"]
	if w.Timeout != 5 || w.MaxRetries != 3 || w.Template != "" || w.AllowInsecure {
		t.Errorf("defaults = %+v, want timeout 5, max_retries 3", w)
	}
	for _, x := range warnings {
		if strings.Contains(x, "webhook") {
			t.Errorf("unexpected warning: %s", x)
		}
	}
}

// max_retries = 0 is kept (no retries); retries is a deprecated alias.
func TestWebhookMaxRetries(t *testing.T) {
	cfg, _, err := loadWebhooks(t, `[webhooks.t]
url = "https://h.example.com/x"
events = ["PROCESS_STATE_FATAL"]
max_retries = 0
`)
	if err != nil || cfg.Webhooks["t"].MaxRetries != 0 {
		t.Fatalf("max_retries = 0: %+v, %v", cfg, err)
	}
	cfg, warnings, err := loadWebhooks(t, `[webhooks.t]
url = "https://h.example.com/x"
events = ["PROCESS_STATE_FATAL"]
retries = 1
`)
	if err != nil || cfg.Webhooks["t"].MaxRetries != 1 {
		t.Fatalf("retries = 1: %+v, %v", cfg, err)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "webhooks.t.retries is deprecated; use max_retries") {
		t.Errorf("no deprecation warning: %v", warnings)
	}
}

func TestWebhookValidationErrors(t *testing.T) {
	base := "[webhooks.t]\nevents = [\"PROCESS_STATE_FATAL\"]\n"
	for body, want := range map[string]string{
		base + "url = \"https://h.example.com/x\"\ntimeout = 0\n":                              "webhooks.t.timeout must be >= 1",
		base + "url = \"https://h.example.com/x\"\nmax_retries = -1\n":                         "webhooks.t.max_retries must be >= 0",
		base + "url = \"https://h.example.com/x\"\ntemplate = \"bogus\"\n":                     "unknown webhook template: bogus",
		base + "url = \"https://h.example.com/x\"\ntemplate = \"pagerduty\"\n":                 "webhooks.t: template pagerduty requires routing_key",
		base + "url = \"http://example.com/hook\"\n":                                           "webhooks.t: URL must use HTTPS (set allow_insecure = true to override)",
		base + "url = \"ftp://x\"\n":                                                           "invalid webhook URL: t",
		base + "url = \"https://\"\n":                                                          "invalid webhook URL: t",
		base + "url = \"${KAHI_TEST_NOPE_UNSET}\"\n":                                           "webhooks.t.url: undefined environment variable: ${KAHI_TEST_NOPE_UNSET}",
		"[webhooks.t]\nurl = \"https://h.example.com/x\"\nevents = [\"PROCESS_LOG_STDOUT\"]\n": "webhooks.t.events: PROCESS_LOG events cannot be sent to webhooks",
	} {
		_, _, err := loadWebhooks(t, body)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("config:\n%s\nerror = %v, want %q", body, err, want)
		}
		if err != nil && strings.Contains(body, "ftp://x") && strings.Contains(err.Error(), "ftp://x") {
			t.Errorf("the URL is printed: %v", err)
		}
	}
}

// Loopback URLs need no TLS; allow_insecure accepts http elsewhere with a
// warning.
func TestWebhookTLSRule(t *testing.T) {
	for _, u := range []string{"http://localhost:8080/h", "http://127.0.0.1:9/h", "http://[::1]:9/h"} {
		_, warnings, err := loadWebhooks(t, "[webhooks.t]\nurl = \""+u+"\"\nevents = [\"PROCESS_STATE_FATAL\"]\n")
		if err != nil {
			t.Errorf("%s: %v", u, err)
		}
		if strings.Contains(strings.Join(warnings, "\n"), "allow_insecure") {
			t.Errorf("%s: unexpected warning %v", u, warnings)
		}
	}
	_, warnings, err := loadWebhooks(t, "[webhooks.t]\nurl = \"http://example.com/h\"\nallow_insecure = true\nevents = [\"PROCESS_STATE_FATAL\"]\n")
	if err != nil || !strings.Contains(strings.Join(warnings, "\n"), "webhooks.t: allow_insecure set, sending over plain HTTP") {
		t.Errorf("allow_insecure: %v, %v", warnings, err)
	}
}

// url, header values and routing_key are expanded at load time.
func TestWebhookExpansion(t *testing.T) {
	t.Setenv("KAHI_TEST_HOOK", "https://h.example.com/secret-path")
	t.Setenv("KAHI_TEST_TOKEN", "tok123")
	t.Setenv("KAHI_TEST_RK", "rk456")
	cfg, _, err := loadWebhooks(t, `[webhooks.t]
url = "${KAHI_TEST_HOOK}"
events = ["PROCESS_STATE_FATAL"]
template = "pagerduty"
routing_key = "${KAHI_TEST_RK}"
headers = { Authorization = "Bearer ${KAHI_TEST_TOKEN}" }
`)
	if err != nil {
		t.Fatal(err)
	}
	w := cfg.Webhooks["t"]
	if w.URL != "https://h.example.com/secret-path" || w.Headers["Authorization"] != "Bearer tok123" || w.RoutingKey != "rk456" {
		t.Errorf("expanded = %+v", w)
	}
}

// The config API shows a webhook URL as scheme and host only, and masks
// routing_key: a Slack-style URL carries its secret in the path.
func TestSanitizedWebhookURLAndRoutingKey(t *testing.T) {
	cfg := &Config{Webhooks: map[string]WebhookConfig{
		"s": {URL: "https://user:pw@hooks.slack.com/services/T0/B0/secret?x=1", RoutingKey: "rk"},
		"b": {URL: "::not a url"},
	}}
	s := Sanitized(cfg)
	if got := s.Webhooks["s"].URL; got != "https://hooks.slack.com/***" {
		t.Errorf("url = %q, want https://hooks.slack.com/***", got)
	}
	if got := s.Webhooks["s"].RoutingKey; got != RedactMask {
		t.Errorf("routing_key = %q, want %q", got, RedactMask)
	}
	if got := s.Webhooks["b"].URL; got != RedactMask {
		t.Errorf("unparseable url = %q, want %q", got, RedactMask)
	}
	if cfg.Webhooks["s"].RoutingKey != "rk" {
		t.Error("Sanitized changed the original")
	}
}

// Unknown event names and an empty list only warn: a notification typo must
// not keep the daemon from starting.
func TestWebhookEventWarnings(t *testing.T) {
	for body, want := range map[string]string{
		"events = [\"process_state\", \"PROCESS_STATE_FATAL\"]\n": "webhooks.t.events: unknown event type: process_state (valid: PROCESS_STATE_STOPPED,",
		"events = []\n": "webhooks.t: no events; the webhook never fires",
		"":              "webhooks.t: no events; the webhook never fires",
	} {
		_, warnings, err := loadWebhooks(t, "[webhooks.t]\nurl = \"https://h.example.com/x\"\n"+body)
		if err != nil {
			t.Errorf("%q: %v", body, err)
			continue
		}
		joined := strings.Join(warnings, "\n")
		if !strings.Contains(joined, want) {
			t.Errorf("%q: warnings %v, want %q", body, warnings, want)
		}
		if strings.Contains(joined, "PROCESS_LOG_STDOUT") {
			t.Errorf("the valid list offers PROCESS_LOG events: %s", joined)
		}
	}
}
