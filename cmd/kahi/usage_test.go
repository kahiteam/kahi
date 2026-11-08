package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

// testTree builds root -> parent -> leaf, the shape of kahi -> ctl -> start.
func testTree() *cobra.Command {
	root := &cobra.Command{Use: "kahi", SilenceErrors: true, SilenceUsage: true}
	parent := &cobra.Command{Use: "ctl"}
	leaf := &cobra.Command{Use: "start", Args: cobra.MinimumNArgs(1), RunE: func(*cobra.Command, []string) error {
		return errors.New("runtime failure")
	}}
	leaf.Flags().Bool("json", false, "")
	parent.AddCommand(leaf)
	root.AddCommand(parent)
	markUsageErrors(root)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	return root
}

func TestUsageErrorsExit2(t *testing.T) {
	tests := []struct {
		args []string
		code int
	}{
		{[]string{"ctl", "start", "web"}, 1}, // runtime failure
		{[]string{"ctl", "start"}, 2},        // argument count
		{[]string{"ctl", "start", "--bogus", "web"}, 2},
		{[]string{"bogus"}, 2},
		{[]string{"ctl", "foo"}, 2},
		{[]string{"ctl", "help", "foo"}, 2},
	}
	for _, tt := range tests {
		root := testTree()
		root.SetArgs(tt.args)
		err := root.Execute()
		if err == nil || exitCode(err) != tt.code {
			t.Errorf("%v: err %v, exit %d; want exit %d", tt.args, err, exitCode(err), tt.code)
		}
	}
	for _, args := range [][]string{{}, {"ctl"}, {"ctl", "help"}, {"ctl", "help", "start"}} {
		root := testTree()
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Errorf("%v: %v, want help and no error", args, err)
		}
	}
}

func TestExitCodeWrapped(t *testing.T) {
	if got := exitCode(errors.New("x")); got != 1 {
		t.Errorf("plain error: exit %d, want 1", got)
	}
	wrapped := errors.Join(errors.New("ctx"), usageError{errors.New("bad")})
	if got := exitCode(wrapped); got != 2 {
		t.Errorf("wrapped usage error: exit %d, want 2", got)
	}
}
