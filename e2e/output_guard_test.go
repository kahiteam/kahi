//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No e2e file hands the test binary's own stdout or stderr to a daemon or
// program: use daemonOutput instead (#238).
func TestNoSubprocessOnTestStdout(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// Built at runtime so this file does not match itself.
	banned := []string{"= os." + "Stdout", "= os." + "Stderr"}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, p := range banned {
				if strings.Contains(line, p) {
					t.Errorf("%s:%d: %s (use daemonOutput, #238)", name, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
