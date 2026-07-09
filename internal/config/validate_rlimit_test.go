package config

import (
	"strings"
	"testing"
)

// validProgram returns a ProgramConfig that passes every non-rlimit check so a
// test can isolate rlimit validation behavior.
func validProgram(env map[string]string) ProgramConfig {
	return ProgramConfig{
		Command:     "/bin/true",
		Priority:    999,
		Autorestart: "unexpected",
		Stopsignal:  "TERM",
		Numprocs:    1,
		Environment: env,
	}
}

func validateProgram(p ProgramConfig) []error {
	cfg := &Config{Programs: map[string]ProgramConfig{"app": p}}
	return Validate(cfg)
}

func TestValidateAcceptsValidRLimit(t *testing.T) {
	cases := []map[string]string{
		{"KAHI_RLIMIT_NOFILE": "65536"},
		{"KAHI_RLIMIT_NOFILE": "1024:65536"},
		{"KAHI_RLIMIT_CORE": "unlimited"},
		{"KAHI_RLIMIT_AS": "-1"},
		{"kahi_rlimit_nproc": "256"}, // key match is case-insensitive
	}
	for _, env := range cases {
		if errs := validateProgram(validProgram(env)); len(errs) != 0 {
			t.Fatalf("env %v: unexpected errors %v", env, errs)
		}
	}
}

func TestValidateRejectsInvalidRLimitValue(t *testing.T) {
	errs := validateProgram(validProgram(map[string]string{
		"KAHI_RLIMIT_NOFILE": "notanumber",
	}))
	if len(errs) == 0 {
		t.Fatal("expected validation error for non-numeric rlimit value")
	}
	msg := errs[0].Error()
	if !strings.Contains(msg, "invalid rlimit KAHI_RLIMIT_NOFILE") || !strings.Contains(msg, "notanumber") {
		t.Fatalf("error = %q, want it to name the resource and value", msg)
	}
}

func TestValidateRejectsInvalidRLimitPair(t *testing.T) {
	errs := validateProgram(validProgram(map[string]string{
		"KAHI_RLIMIT_NOFILE": "1024:bad",
	}))
	if len(errs) == 0 {
		t.Fatal("expected validation error for malformed soft:hard pair")
	}
}

func TestValidateRejectsUnknownRLimitResource(t *testing.T) {
	errs := validateProgram(validProgram(map[string]string{
		"KAHI_RLIMIT_BOGUS": "1024",
	}))
	if len(errs) == 0 {
		t.Fatal("expected validation error for unknown rlimit resource")
	}
	if !strings.Contains(errs[0].Error(), "unknown resource") {
		t.Fatalf("error = %q, want unknown resource", errs[0].Error())
	}
}

func TestValidateIgnoresNonRLimitEnv(t *testing.T) {
	errs := validateProgram(validProgram(map[string]string{
		"APP_KEY":     "value",
		"PATH":        "/usr/bin",
		"KAHI_MARKER": "not-an-rlimit",
	}))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors for ordinary environment: %v", errs)
	}
}
