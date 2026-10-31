package config

import (
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// signals lists the signals the signal API and `kahi ctl signal` accept,
// keyed by name without the SIG prefix.
var signals = map[string]syscall.Signal{
	"TERM": syscall.SIGTERM,
	"HUP":  syscall.SIGHUP,
	"INT":  syscall.SIGINT,
	"QUIT": syscall.SIGQUIT,
	"KILL": syscall.SIGKILL,
	"USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2,
	"STOP": syscall.SIGSTOP,
	"CONT": syscall.SIGCONT,
}

// stopSignals is the subset allowed as a program's stopsignal.
var stopSignals = map[string]bool{
	"TERM": true, "HUP": true, "INT": true, "QUIT": true,
	"KILL": true, "USR1": true, "USR2": true,
}

// ParseSignal resolves a signal name, with or without the SIG prefix and in
// any case, and returns the signal and its <signal.h> name ("SIGTERM").
// Signal names follow <signal.h> in every output (ADR-038, #215).
func ParseSignal(name string) (syscall.Signal, string, bool) {
	sig, ok := signals[bareSignalName(name)]
	if !ok {
		return 0, "", false
	}
	return sig, SignalName(sig), true
}

// IsStopSignal reports whether name is allowed as a stopsignal.
func IsStopSignal(name string) bool {
	return stopSignals[bareSignalName(name)]
}

// SignalName returns the <signal.h> name of sig ("SIGKILL").
func SignalName(sig syscall.Signal) string {
	return unix.SignalName(sig)
}

func bareSignalName(name string) string {
	return strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(name)), "SIG")
}
