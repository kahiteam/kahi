package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kahiteam/kahi/internal/ctl"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// runAttach runs kahi ctl attach (FUNC-034, ADR-044). On a terminal the
// input side goes raw once the daemon accepts the session, and every exit
// path restores it; a signal to the client ends the session.
func runAttach(cmd *cobra.Command, c *ctl.Client, name string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	stderr := cmd.ErrOrStderr()
	tty := term.IsTerminal(int(os.Stdin.Fd()))
	restore := func() {}
	defer func() { restore() }()
	end, err := c.Attach(ctx, name, os.Stdin, cmd.OutOrStdout(), stderr, ctl.AttachOptions{
		TTY: tty,
		OnAttached: func() {
			if !tty {
				return
			}
			if r, err := ctl.RawInput(ctl.FDTerminal(os.Stdin.Fd())); err == nil {
				restore = r
			}
			_, _ = fmt.Fprintf(stderr, "attached to %s (Ctrl+] to detach)\r\n", name)
		},
	})
	restore()
	restore = func() {}
	if err != nil {
		return err
	}
	switch end {
	case ctl.AttachDetached:
		_, _ = fmt.Fprintf(stderr, "detached from %s\n", name)
	case ctl.AttachExited:
		_, _ = fmt.Fprintf(stderr, "process %s exited\n", name)
	}
	return nil
}
