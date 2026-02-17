package main

import (
	"fmt"
	"runtime"

	"github.com/kahiteam/kahi/internal/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	RunE: func(cmd *cobra.Command, args []string) error {
		goVer := version.GoVersion
		if goVer == "" {
			goVer = runtime.Version()
		}
		w := cmd.OutOrStdout()
		for _, line := range []string{
			fmt.Sprintf("kahi %s", version.Version),
			fmt.Sprintf("  commit:  %s", version.Commit),
			fmt.Sprintf("  built:   %s", version.Date),
			fmt.Sprintf("  go:      %s", goVer),
			fmt.Sprintf("  os/arch: %s/%s", runtime.GOOS, runtime.GOARCH),
			fmt.Sprintf("  fips:    %s", version.FIPS),
		} {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
