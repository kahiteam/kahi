package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FUNC-086: *_capture_maxbytes takes the logfile_maxbytes syntax and must be
// between 1KB and 16MB inclusive; unset means the 64 KB default.
func TestCaptureMaxbytesValidation(t *testing.T) {
	tests := []struct {
		value   string
		wantErr string
	}{
		{`"1KB"`, ""},
		{`"1024"`, ""},
		{`"64KB"`, ""},
		{`"1MB"`, ""},
		{`"16MB"`, ""},
		{`"16777216"`, ""},
		{`"1023"`, "must be between 1KB and 16MB"},
		{`"100"`, "must be between 1KB and 16MB"},
		{`"0"`, "must be between 1KB and 16MB"},
		{`"16777217"`, "must be between 1KB and 16MB"},
		{`"32MB"`, "must be between 1KB and 16MB"},
		{`"1GB"`, "must be between 1KB and 16MB"},
		{`"abc"`, "invalid size abc"},
		{`"-5KB"`, "invalid size -5KB"},
		{`"9999999999999GB"`, "invalid size 9999999999999GB"},
	}
	for _, key := range []string{"stdout_capture_maxbytes", "stderr_capture_maxbytes"} {
		for _, tt := range tests {
			t.Run(key+"="+tt.value, func(t *testing.T) {
				data := "[programs.p]\ncommand = \"/bin/true\"\n" + key + " = " + tt.value + "\n"
				_, _, err := LoadBytes([]byte(data), "test.toml")
				if tt.wantErr == "" {
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					return
				}
				want := "programs.p." + key + ": " + tt.wantErr
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to contain %q", err, want)
				}
			})
		}
	}
}

func TestCaptureMaxbytesUnsetIsValid(t *testing.T) {
	cfg, _, err := LoadBytes([]byte("[programs.p]\ncommand = \"/bin/true\"\n"), "test.toml")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Programs["p"].StdoutCaptureMaxbytes; got != "" {
		t.Errorf("StdoutCaptureMaxbytes = %q, want unset", got)
	}
}

// With redirect_stderr there is no stderr buffer, so its size does nothing.
func TestCaptureMaxbytesRedirectWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte(`[programs.p]
command = "/bin/true"
redirect_stderr = true
stdout_capture_maxbytes = "1MB"
stderr_capture_maxbytes = "1MB"
`), 0644); err != nil {
		t.Fatal(err)
	}
	_, warnings, err := LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "programs.p.stderr_capture_maxbytes has no effect with redirect_stderr") {
		t.Errorf("no redirect warning; warnings:\n%s", joined)
	}
	if strings.Contains(joined, "stdout_capture_maxbytes") || strings.Contains(joined, "not implemented yet") {
		t.Errorf("unexpected warning; warnings:\n%s", joined)
	}
}
