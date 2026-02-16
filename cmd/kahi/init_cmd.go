package main

import (
	"fmt"
	"os"

	"github.com/kahidev/kahi/internal/config"
	"github.com/spf13/cobra"
)

var initOutput string
var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a sample kahi.toml config file",
	RunE: func(cmd *cobra.Command, args []string) error {
		content := config.DefaultConfigTOML

		if initOutput == "" {
			_, err := fmt.Fprint(cmd.OutOrStdout(), content)
			return err
		}

		if !initForce {
			if _, err := os.Stat(initOutput); err == nil {
				return fmt.Errorf("file %s already exists; use --force to overwrite", initOutput)
			}
		}

		if err := os.WriteFile(initOutput, []byte(content), 0644); err != nil {
			return fmt.Errorf("cannot write config: %w", err)
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", initOutput)
		return err
	},
}

func init() {
	initCmd.Flags().StringVarP(&initOutput, "output", "o", "", "write config to file instead of stdout")
	initCmd.Flags().BoolVar(&initForce, "force", false, "overwrite existing file")
	rootCmd.AddCommand(initCmd)
}
