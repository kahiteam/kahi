// Package supervisor coordinates multiple managed processes, signal
// handling, and the main event loop for the Kahi daemon.
package supervisor

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// SignalQueue captures OS signals for deferred processing in the main loop.
type SignalQueue struct {
	C      <-chan os.Signal
	ch     chan os.Signal
	logger *slog.Logger
}

// NewSignalQueue creates a signal queue with a buffer of 16 signals.
// It registers for SIGTERM, SIGINT, SIGQUIT, SIGHUP, SIGUSR2, and SIGCHLD.
func NewSignalQueue(logger *slog.Logger) *SignalQueue {
	ch := make(chan os.Signal, 16)
	signal.Notify(ch,
		syscall.SIGTERM,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGHUP,
		syscall.SIGUSR2,
		syscall.SIGCHLD,
	)
	return &SignalQueue{
		C:      ch,
		ch:     ch,
		logger: logger,
	}
}

// Stop deregisters signal notifications and closes the channel.
func (sq *SignalQueue) Stop() {
	signal.Stop(sq.ch)
}

// getuid is the function used to retrieve the current user ID.
// It defaults to os.Getuid and can be overridden in tests.
var getuid = os.Getuid

// RootWarning logs a warning if the process is running as root (uid 0)
// without a configured user for privilege dropping.
func RootWarning(logger *slog.Logger, userConfigured bool) {
	if getuid() != 0 {
		return
	}
	if userConfigured {
		return
	}
	logger.Warn("running as root without user config; consider setting [supervisor] user for privilege dropping")
}
