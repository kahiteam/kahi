package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

var hashPasswordCmd = &cobra.Command{
	Use:   "hash-password",
	Short: "Hash a password using bcrypt",
	Long:  "Generate a bcrypt password hash for use in kahi.toml configuration.",
	RunE: func(cmd *cobra.Command, args []string) error {
		password, err := readPassword()
		if err != nil {
			return err
		}

		if len(password) == 0 {
			return fmt.Errorf("password cannot be empty")
		}

		hash, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("bcrypt error: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), string(hash))
		return nil
	},
}

func readPassword() ([]byte, error) {
	// Check if stdin is a pipe (non-interactive).
	stat, _ := os.Stdin.Stat()
	if stat != nil && (stat.Mode()&os.ModeCharDevice) == 0 {
		// Piped input: read first line.
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			return []byte(strings.TrimSpace(scanner.Text())), nil
		}
		return nil, fmt.Errorf("no input received")
	}

	// Interactive: prompt with echo suppressed.
	fmt.Fprint(os.Stderr, "Password: ")
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr) // newline after suppressed input
	if err != nil {
		return nil, fmt.Errorf("failed to read password: %w", err)
	}
	return password, nil
}

func init() {
	rootCmd.AddCommand(hashPasswordCmd)
}
