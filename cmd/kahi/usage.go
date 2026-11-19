package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// usageError marks a command-line usage error. Every kahi command exits 0 on
// success, 1 on a runtime failure and 2 on a usage error (ADR-035, #213).
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

// silentError ends a command with exit 1 after it printed its own errors;
// main prints nothing for it.
type silentError struct{ err error }

func (e silentError) Error() string { return e.err.Error() }
func (e silentError) Unwrap() error { return e.err }

// exitCode maps the error returned by the root command to the exit code.
func exitCode(err error) int {
	var u usageError
	if errors.As(err, &u) {
		return 2
	}
	return 1
}

// markUsageErrors turns every usage error of cmd and its subcommands into a
// usageError: argument validation, flag parsing and unknown subcommands.
// cobra rejects an unknown subcommand only on the root; a parent such as
// `kahi ctl` would print help and exit 0 for `kahi ctl foo`, so parents get
// their own handler.
func markUsageErrors(cmd *cobra.Command) {
	if cmd == cmd.Root() {
		cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	}
	// A command whose Use names no arguments takes none: cobra's default
	// accepts anything, so `kahi ctl reload web` used to reload everything
	// and exit 0 (FUNC-031: a wrong argument count exits 2).
	if cmd.Args == nil && cmd.Runnable() && !strings.Contains(cmd.Use, " ") {
		cmd.Args = cobra.NoArgs
	}
	if validate := cmd.Args; validate != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			if err := validate(c, args); err != nil {
				return usageError{err}
			}
			return nil
		}
	}
	if !cmd.Runnable() && cmd.HasSubCommands() {
		cmd.Args = cobra.ArbitraryArgs
		cmd.RunE = func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return c.Help()
			}
			// `kahi ctl help [command]`, as supervisorctl users type it.
			if args[0] == "help" {
				if len(args) == 1 {
					return c.Help()
				}
				if sub, _, err := c.Find(args[1:]); err == nil && sub != c {
					return sub.Help()
				}
				args = args[1:]
			}
			return usageError{fmt.Errorf("unknown command %q for %q", args[0], c.CommandPath())}
		}
	}
	for _, sub := range cmd.Commands() {
		markUsageErrors(sub)
	}
}
