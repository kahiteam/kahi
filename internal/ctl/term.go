//go:build darwin || linux

package ctl

import "golang.org/x/sys/unix"

// Terminal reads and sets a terminal's termios (ADR-044); a fake stands in
// for it in tests.
type Terminal interface {
	Get() (*unix.Termios, error)
	Set(*unix.Termios) error
}

// FDTerminal is the terminal on a file descriptor.
type FDTerminal int

// Get reads the termios.
func (fd FDTerminal) Get() (*unix.Termios, error) {
	return unix.IoctlGetTermios(int(fd), ioctlGetTermios)
}

// Set writes the termios.
func (fd FDTerminal) Set(t *unix.Termios) error {
	return unix.IoctlSetTermios(int(fd), ioctlSetTermios, t)
}

// RawInput puts only the input side in raw mode (FUNC-034): no line
// buffering (ICANON), no echo (ECHO), no signals from ^C ^Z ^\ (ISIG), so
// every byte reaches the process; output processing (OPOST) stays on, so
// "\n" still returns the carriage. restore puts the old state back.
func RawInput(t Terminal) (restore func(), err error) {
	old, err := t.Get()
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := t.Set(&raw); err != nil {
		return nil, err
	}
	saved := *old
	return func() { _ = t.Set(&saved) }, nil
}
