package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeReviewConf(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A substituted value is not expanded again (FUNC-012): $$ and %% inside
// an environment variable's value, and ${...} in the config directory's
// name, stay as they are.
func TestExpandSubstitutedValuesNotRescanned(t *testing.T) {
	t.Setenv("ZZ_REVIEW_PW", "ab$$cd%%ef${X}")
	p := writeReviewConf(t, t.TempDir(), "[programs.a]\ncommand = \"/bin/true\"\nenvironment = { PW = \"${ZZ_REVIEW_PW}\" }\n")
	cfg, _, err := LoadWithIncludes(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Programs["a"].Environment["PW"]; got != "ab$$cd%%ef${X}" {
		t.Errorf("PW %q", got)
	}

	dir := filepath.Join(t.TempDir(), "x${ZZ_REVIEW_NOPE}")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p = writeReviewConf(t, dir, "[programs.a]\ncommand = \"/bin/true\"\ndirectory = \"%(here)s\"\n")
	cfg, _, err = LoadWithIncludes(p)
	if err != nil {
		t.Fatalf("%%(here)s with ${ in the path: %v", err)
	}
	if got := cfg.Programs["a"].Directory; got != dir {
		t.Errorf("directory %q, want %q", got, dir)
	}
}

// With numprocs > 1, %% stays a literal % and a substituted % is never
// read as %(process_num)d by the per-instance pass.
func TestExpandEscapesSurviveNumprocs(t *testing.T) {
	t.Setenv("ZZ_REVIEW_FMT", "%(process_num)d")
	p := writeReviewConf(t, t.TempDir(), "[programs.a]\ncommand = \"echo %%(process_num)d %(process_num)d ${ZZ_REVIEW_FMT} 100%%\"\nnumprocs = 2\nprocess_name = \"a_%(process_num)d\"\n")
	cfg, _, err := LoadWithIncludes(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 1} {
		got := ExpandProcessNum(cfg.Programs["a"].Command, n)
		want := "echo %(process_num)d " + string(rune('0'+n)) + " %(process_num)d 100%"
		if got != want {
			t.Errorf("instance %d: %q, want %q", n, got, want)
		}
	}
}

// Bounds: values that overflowed or panicked are config errors.
func TestValidateUpperBounds(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"numprocs = 4611686018427387904\nprocess_name = \"a_%(process_num)d\"\n", "numprocs must be <= 10000"},
		{"numprocs = 2\nnumprocs_start = 9223372036854775807\nprocess_name = \"a_%(process_num)d\"\n", "numprocs_start must be between 0 and 1000000000"},
		{"numprocs_start = -1\n", "numprocs_start must be between 0 and 1000000000"},
		{"stopwaitsecs = 9300000000\n", "stopwaitsecs must be <= 31536000"},
		{"startsecs = 31536001\n", "startsecs must be <= 31536000"},
	} {
		p := writeReviewConf(t, t.TempDir(), "[programs.a]\ncommand = \"/bin/true\"\n"+tc.body)
		if _, _, err := LoadWithIncludes(p); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v, want %q", tc.body, err, tc.want)
		}
	}
	p := writeReviewConf(t, t.TempDir(), "[supervisor]\nshutdown_timeout = 9300000000\n")
	if _, _, err := LoadWithIncludes(p); err == nil || !strings.Contains(err.Error(), "supervisor.shutdown_timeout must be <= 31536000") {
		t.Errorf("shutdown_timeout: %v", err)
	}
}

// An unclosed reference names its position, not the (possibly secret)
// value.
func TestExpandErrorsDoNotEchoValue(t *testing.T) {
	p := writeReviewConf(t, t.TempDir(), "[programs.a]\ncommand = \"/bin/true\"\nenvironment = { T = \"Bearer sk-live-abc ${TOKEN\" }\n")
	_, _, err := LoadWithIncludes(p)
	if err == nil || strings.Contains(err.Error(), "sk-live-abc") || !strings.Contains(err.Error(), "unclosed environment variable reference") {
		t.Errorf("err %v", err)
	}
}
