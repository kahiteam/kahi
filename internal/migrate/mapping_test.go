package migrate

import (
	"testing"
)

func TestMapProgramOptionStartsecs(t *testing.T) {
	opt := MapProgramOption("startsecs", "10")
	if opt.Key != "startsecs" {
		t.Errorf("key = %q, want startsecs", opt.Key)
	}
	if opt.Value != "10" {
		t.Errorf("value = %q, want 10", opt.Value)
	}
	if opt.Unsupported {
		t.Error("should not be unsupported")
	}
}

func TestMapProgramOptionAutorestart(t *testing.T) {
	opt := MapProgramOption("autorestart", "unexpected")
	if opt.Value != `"unexpected"` {
		t.Errorf("value = %q, want quoted unexpected", opt.Value)
	}
}

func TestMapProgramOptionUnsupported(t *testing.T) {
	opt := MapProgramOption("serverurl", "AUTO")
	if !opt.Unsupported {
		t.Error("serverurl should be unsupported")
	}
	if opt.Comment == "" {
		t.Error("should have UNSUPPORTED comment")
	}
}

func TestMapProgramOptionUnknown(t *testing.T) {
	opt := MapProgramOption("xmlrpc_timeout", "30")
	if !opt.Unsupported {
		t.Error("unknown option should be unsupported")
	}
}

func TestMapProgramOptionRenamed(t *testing.T) {
	opt := MapSupervisordOption("loglevel", "debug")
	if opt.Key != "log_level" {
		t.Errorf("key = %q, want log_level", opt.Key)
	}
	if opt.Comment == "" {
		t.Error("renamed option should have comment")
	}
}

func TestMapByteSizePreserved(t *testing.T) {
	opt := MapProgramOption("stdout_logfile_maxbytes", "50MB")
	if opt.Value != `"50MB"` {
		t.Errorf("value = %q, want quoted 50MB", opt.Value)
	}
}

func TestNormalizeSignal(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"TERM", "TERM"},
		{"term", "TERM"},
		{"SIGTERM", "TERM"},
		{"sigterm", "TERM"},
		{"HUP", "HUP"},
		{"SIGHUP", "HUP"},
	}
	for _, tt := range tests {
		got := NormalizeSignal(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeSignal(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMapProgramOptionExitcodes(t *testing.T) {
	opt := MapProgramOption("exitcodes", "0,2")
	if opt.Value != "[0, 2]" {
		t.Errorf("value = %q, want [0, 2]", opt.Value)
	}
}

func TestMapGroupOptionPrograms(t *testing.T) {
	opt := MapGroupOption("programs", "web,api")
	if opt.Value != `["web", "api"]` {
		t.Errorf("value = %q, want [\"web\", \"api\"]", opt.Value)
	}
}

func TestMapGroupOptionPriority(t *testing.T) {
	opt := MapGroupOption("priority", "100")
	if opt.Value != "100" {
		t.Errorf("value = %q, want 100", opt.Value)
	}
}

func TestMapProgramOptionSignal(t *testing.T) {
	opt := MapProgramOption("stopsignal", "SIGTERM")
	if opt.Value != `"TERM"` {
		t.Errorf("value = %q, want \"TERM\"", opt.Value)
	}
}
