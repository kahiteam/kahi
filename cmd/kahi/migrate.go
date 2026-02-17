package main

import (
	"fmt"
	"os"

	"github.com/kahiteam/kahi/internal/migrate"
	"github.com/spf13/cobra"
)

var (
	migrateOutput string
	migrateForce  bool
	migrateDryRun bool
)

var migrateCmd = &cobra.Command{
	Use:   "migrate <supervisord.conf>",
	Short: "Convert supervisord.conf to kahi.toml",
	Long:  "Parse a supervisord.conf INI file and convert it to Kahi TOML format.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputPath := args[0]
		opts := migrate.Options{
			Output: migrateOutput,
			Force:  migrateForce,
			DryRun: migrateDryRun,
		}

		result, err := migrate.Migrate(inputPath, opts)
		if err != nil {
			return err
		}

		// Print warnings to stderr.
		for _, w := range result.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}

		// Write output.
		if err := migrate.WriteResult(result, opts, cmd.OutOrStdout()); err != nil {
			return err
		}

		// Print validation errors to stderr.
		for _, e := range result.ValidErrs {
			fmt.Fprintf(os.Stderr, "validation: %s\n", e)
		}

		if migrateOutput != "" && !migrateDryRun {
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", migrateOutput)
		}

		return nil
	},
}

func init() {
	migrateCmd.Flags().StringVarP(&migrateOutput, "output", "o", "", "write TOML to file instead of stdout")
	migrateCmd.Flags().BoolVar(&migrateForce, "force", false, "overwrite existing output file")
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "preview output without writing files")
	rootCmd.AddCommand(migrateCmd)
}
