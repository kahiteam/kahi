package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// daemonChildEnv marks the background daemon started by kahi daemon -d. Its
// value is the descriptor of the pipe the child reports its start on.
const daemonChildEnv = "KAHI_DAEMON_CHILD"

// daemonize runs kahi daemon again as a background process in a new session,
// with its standard streams on /dev/null, and waits until it reports that it
// is running or why it failed (FUNC-064). Go cannot fork safely, so this
// re-executes the binary instead of double-forking.
func daemonize(stdout io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("daemonize: %w", err)
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("daemonize: %w", err)
	}
	defer func() { _ = devnull.Close() }()
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("daemonize: %w", err)
	}
	defer func() { _ = r.Close() }()

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), daemonChildEnv+"=3")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	cmd.ExtraFiles = []*os.File{w}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = w.Close()
		return fmt.Errorf("daemonize: %w", err)
	}
	_ = w.Close()

	// The child closes its end once it runs or fails; until then this blocks.
	report, _ := io.ReadAll(r)
	msg := strings.TrimSpace(string(report))
	if msg == "ok" {
		_, _ = fmt.Fprintf(stdout, "kahi daemon started (pid %d)\n", cmd.Process.Pid)
		return cmd.Process.Release()
	}
	_ = cmd.Wait()
	if msg == "" {
		return errors.New("daemon exited during startup")
	}
	return errors.New(msg)
}

// daemonChildReport returns the pipe a daemonized child reports on, or nil
// when the daemon runs in the foreground. The variable is cleared so that
// supervised processes do not inherit it.
func daemonChildReport() *os.File {
	v := os.Getenv(daemonChildEnv)
	if v == "" {
		return nil
	}
	_ = os.Unsetenv(daemonChildEnv)
	if v != "3" {
		return nil
	}
	// Inherited without close-on-exec: the processes the daemon starts would
	// keep the pipe open and the parent would wait for them.
	syscall.CloseOnExec(3)
	return os.NewFile(3, "kahi-daemon-report")
}
