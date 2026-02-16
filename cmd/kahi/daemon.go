package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the Kahi supervisor daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("daemon mode")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(daemonCmd)
}
