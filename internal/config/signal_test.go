package config

import (
	"syscall"
	"testing"
)

func TestParseSignal(t *testing.T) {
	tests := []struct {
		input string
		sig   syscall.Signal
		name  string
	}{
		{"TERM", syscall.SIGTERM, "SIGTERM"},
		{"sigterm", syscall.SIGTERM, "SIGTERM"},
		{" HUP ", syscall.SIGHUP, "SIGHUP"},
		{"SIGKILL", syscall.SIGKILL, "SIGKILL"},
		{"usr2", syscall.SIGUSR2, "SIGUSR2"},
		{"STOP", syscall.SIGSTOP, "SIGSTOP"},
		{"CONT", syscall.SIGCONT, "SIGCONT"},
	}
	for _, tt := range tests {
		sig, name, ok := ParseSignal(tt.input)
		if !ok || sig != tt.sig || name != tt.name {
			t.Errorf("ParseSignal(%q) = %v, %q, %v; want %v, %q", tt.input, sig, name, ok, tt.sig, tt.name)
		}
	}
	for _, bad := range []string{"", "BOGUS", "SIG", "SIGSIGTERM", "CHLD"} {
		if _, _, ok := ParseSignal(bad); ok {
			t.Errorf("ParseSignal(%q) accepted", bad)
		}
	}
}

func TestIsStopSignal(t *testing.T) {
	for _, ok := range []string{"TERM", "SIGTERM", "hup", "INT", "QUIT", "KILL", "USR1", "SIGUSR2"} {
		if !IsStopSignal(ok) {
			t.Errorf("IsStopSignal(%q) = false", ok)
		}
	}
	for _, bad := range []string{"STOP", "SIGCONT", "BOGUS", ""} {
		if IsStopSignal(bad) {
			t.Errorf("IsStopSignal(%q) = true", bad)
		}
	}
}
