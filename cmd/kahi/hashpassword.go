package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var hashPasswordCmd = &cobra.Command{
	Use:   "hash-password",
	Short: "Hash a password using bcrypt",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("hash-password mode")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(hashPasswordCmd)
}
