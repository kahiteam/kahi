package main

import (
	"fmt"
	"os"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/spf13/cobra"
)

var initOutput string
var initStdout bool
var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a sample kahi.toml config file",
	RunE: func(cmd *cobra.Command, args []string) error {
		content := config.DefaultConfigTOML

		if initStdout {
			_, err := fmt.Fprint(cmd.OutOrStdout(), content)
			return err
		}

		outPath := initOutput
		if outPath == "" {
			outPath = "kahi.toml"
		}

		if !initForce {
			if _, err := os.Stat(outPath); err == nil {
				return fmt.Errorf("file %s already exists; use --force to overwrite", outPath)
			}
		}

		if err := os.WriteFile(outPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("cannot write config: %w", err)
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", outPath)
		return err
	},
}

func init() {
	initCmd.Flags().StringVarP(&initOutput, "output", "o", "", "write config to file (default: kahi.toml)")
	initCmd.Flags().BoolVar(&initStdout, "stdout", false, "print config to stdout instead of writing a file")
	initCmd.Flags().BoolVar(&initForce, "force", false, "overwrite existing file")
	rootCmd.AddCommand(initCmd)
}
