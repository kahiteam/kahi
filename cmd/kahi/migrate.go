package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Convert supervisord.conf to kahi.toml",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("migrate mode")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
