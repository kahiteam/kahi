// Package testutil provides shared test helpers.
package testutil

import (
	"os"
	"testing"
)

// TempDir creates a temporary directory for testing and registers cleanup.
func TempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kahi-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
