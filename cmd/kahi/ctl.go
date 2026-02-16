package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var ctlCmd = &cobra.Command{
	Use:   "ctl",
	Short: "Control a running Kahi daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("control mode")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(ctlCmd)
}
