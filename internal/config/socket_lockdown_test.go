package config

import (
	"strings"
	"testing"
)

// TestControlSocketOwnerOnly covers SEC-022: the Unix control socket is locked
// to the service identity. server.unix.chown is rejected and any chmod granting
// group or other access is rejected, while owner-only modes load cleanly.
func TestControlSocketOwnerOnly(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string // substring; empty means load must succeed
	}{
		{
			name:    "default has no server.unix and loads",
			toml:    "[supervisor]\nlog_level = \"info\"\n",
			wantErr: "",
		},
		{
			name:    "explicit owner-only 0700 loads",
			toml:    "[server.unix]\nchmod = \"0700\"\n",
			wantErr: "",
		},
		{
			name:    "owner-only 0600 loads",
			toml:    "[server.unix]\nchmod = \"0600\"\n",
			wantErr: "",
		},
		{
			name:    "group access 0770 rejected",
			toml:    "[server.unix]\nchmod = \"0770\"\n",
			wantErr: "control socket must be owner-only (0700); got 0770",
		},
		{
			name:    "other access 0666 rejected",
			toml:    "[server.unix]\nchmod = \"0666\"\n",
			wantErr: "control socket must be owner-only (0700); got 0666",
		},
		{
			name:    "group read 0740 rejected",
			toml:    "[server.unix]\nchmod = \"0740\"\n",
			wantErr: "control socket must be owner-only (0700); got 0740",
		},
		{
			name:    "chown rejected as unsupported",
			toml:    "[server.unix]\nchown = \"nobody:nogroup\"\n",
			wantErr: "server.unix.chown is not supported; socket is owner-only",
		},
		{
			name:    "malformed chmod rejected",
			toml:    "[server.unix]\nchmod = \"not-octal\"\n",
			wantErr: "server.unix.chmod must be an octal mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := LoadBytes([]byte(tt.toml), "test.toml")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected load to succeed, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestValidateUnixServerReportsAllErrors verifies chown and a shared chmod are
// each reported so an operator sees every problem in one pass.
func TestValidateUnixServerReportsAllErrors(t *testing.T) {
	cfg := &Config{}
	cfg.Server.Unix.Chown = "nobody:nogroup"
	cfg.Server.Unix.Chmod = "0777"

	errs := Validate(cfg)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d: %v", len(errs), errs)
	}

	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	if !strings.Contains(joined, "server.unix.chown is not supported") {
		t.Errorf("missing chown error: %v", errs)
	}
	if !strings.Contains(joined, "control socket must be owner-only (0700); got 0777") {
		t.Errorf("missing chmod error: %v", errs)
	}
}
