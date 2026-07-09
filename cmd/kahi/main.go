package main

import (
	"fmt"
	"os"

	"github.com/kahiteam/kahi/internal/process"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "kahi",
	Short:         "Kahi -- lightweight process supervisor",
	Long:          "Kahi is a modern process supervisor for POSIX systems.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func main() {
	// Spawn-trampoline invocations re-exec the target with configured rlimits
	// and umask applied; they must bypass cobra command dispatch entirely.
	if process.IsChildInit(os.Args) {
		if err := process.RunChildInit(os.Args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(127)
		}
		return // unreachable: RunChildInit execs on success
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
