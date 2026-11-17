package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FUNC-055 / ADR-042: [eventlisteners.x] is a program plus events,
// buffer_size (10) and result_timeout (60).
func TestEventListenerDefaults(t *testing.T) {
	cfg, warnings, err := loadWebhooks(t, `[eventlisteners.l]
command = "/bin/cat"
events = ["PROCESS_STATE_EXITED"]
`)
	if err != nil {
		t.Fatal(err)
	}
	l := cfg.EventListeners["l"]
	if l.BufferSize != 10 || l.ResultTimeout != 60 {
		t.Errorf("buffer_size %d, result_timeout %d; want 10, 60", l.BufferSize, l.ResultTimeout)
	}
	if l.Numprocs != 1 || l.Startsecs != 1 || l.Stopsignal != "SIGTERM" || l.Priority != 999 {
		t.Errorf("program defaults not applied: %+v", l.ProgramConfig)
	}
	// Pools run since plan step 7b: no not-run warning.
	if strings.Contains(strings.Join(warnings, "\n"), "no event listener runs") {
		t.Errorf("stale not-run warning: %v", warnings)
	}
}

func TestEventListenerValidationErrors(t *testing.T) {
	base := "[eventlisteners.l]\ncommand = \"/bin/cat\"\nevents = [\"PROCESS_STATE_EXITED\"]\n"
	for body, want := range map[string]string{
		base + "buffer_size = 0\n":                         "eventlisteners.l.buffer_size must be >= 1",
		base + "result_timeout = 0\n":                      "eventlisteners.l.result_timeout must be >= 1",
		base + "redirect_stderr = true\n":                  "eventlisteners.l.redirect_stderr: not allowed, stdout carries the protocol",
		base + "stopwaitsecs = 0\n":                        "eventlisteners.l: stopwaitsecs must be >= 1",
		"[eventlisteners.l]\nevents = []\n":                "eventlisteners.l: command is required",
		base + "\n[programs.l]\ncommand = \"/bin/true\"\n": "eventlisteners.l: name already used by programs.l",
		base + "\n[programs.w]\ncommand = \"/bin/true\"\n\n[groups.g]\nprograms = [\"w\", \"l\"]\n": "groups.g: l is an event listener pool; a pool cannot be in a group",
		base + "\n[programs.w]\ncommand = \"/bin/true\"\n\n[groups.l]\nprograms = [\"w\"]\n":        "groups.l: name collides with event listener pool l",
		base + "\n[programs.m]\ncommand = \"/bin/true\"\nprocess_name = \"l\"\n":                    "duplicate process name: l",
	} {
		_, _, err := loadWebhooks(t, body)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("config:\n%s\nerror = %v, want %q", body, err, want)
		}
	}
}

// An unknown event type or no events warns (decided 2026-11-16, like
// webhooks); stdout output keys have no effect on a pool.
func TestEventListenerWarnings(t *testing.T) {
	_, warnings, err := loadWebhooks(t, `[eventlisteners.l]
command = "/bin/cat"
events = ["BOGUS", "PROCESS_STATE_EXITED"]
stdout_logfile = "/tmp/x.log"
stdout_events_enabled = true

[eventlisteners.e]
command = "/bin/cat"
`)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{
		"eventlisteners.l.events: unknown event type: BOGUS (valid: PROCESS_STATE_STOPPED,",
		"eventlisteners.l.stdout_logfile has no effect: stdout carries the protocol",
		"eventlisteners.l.stdout_events_enabled has no effect: stdout carries the protocol",
		"eventlisteners.e: no events; the pool never receives any",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("no warning %q; warnings:\n%s", want, joined)
		}
	}
	// Defaults are not set by the user: no warning for maxbytes/backups.
	if strings.Contains(joined, "stdout_logfile_maxbytes") || strings.Contains(joined, "stdout_logfile_backups") {
		t.Errorf("warned for a default: %s", joined)
	}
	// Pools may subscribe to PROCESS_LOG events, unlike webhooks.
	if strings.Contains(joined, "PROCESS_LOG") && !strings.Contains(joined, "valid:") {
		t.Errorf("unexpected PROCESS_LOG warning: %s", joined)
	}
}

func TestEventListenerExpansionAndRedaction(t *testing.T) {
	t.Setenv("KAHI_TEST_LISTENER_VALUE", "value-l")
	cfg, _, err := loadWebhooks(t, `[eventlisteners.l]
command = "/bin/cat %(program_name)s"
events = ["PROCESS_STATE_EXITED"]
[eventlisteners.l.environment]
SETTING = "${KAHI_TEST_LISTENER_VALUE}"
`)
	if err != nil {
		t.Fatal(err)
	}
	l := cfg.EventListeners["l"]
	if l.Command != "/bin/cat l" || l.Environment["SETTING"] != "value-l" {
		t.Errorf("expanded = %q, %v", l.Command, l.Environment)
	}
	if got := Sanitized(cfg).EventListeners["l"].Environment["SETTING"]; got != RedactMask {
		t.Errorf("environment in the config view = %q, want masked", got)
	}
}

// A pool defined in two files is an error naming both.
func TestEventListenerIncludeDuplicate(t *testing.T) {
	dir := t.TempDir()
	pool := "[eventlisteners.l]\ncommand = \"/bin/cat\"\nevents = [\"PROCESS_STATE_EXITED\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "inc.toml"), []byte(pool), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(main, []byte("include = [\"inc.toml\"]\n\n"+pool), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadWithIncludes(main)
	if err == nil || !strings.Contains(err.Error(), `duplicate event listener name "l": defined in both`) {
		t.Errorf("error = %v", err)
	}

	// From an include alone it merges.
	if err := os.WriteFile(main, []byte("include = [\"inc.toml\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadWithIncludes(main)
	if err != nil || cfg.EventListeners["l"].Command != "/bin/cat" {
		t.Errorf("merged = %+v, %v", cfg, err)
	}
}
