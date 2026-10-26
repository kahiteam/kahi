//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kahi ctl reread and update print the new config's warnings, so a user who
// sets an ignored key learns it has no effect (#140).
func TestCLI_RereadAndUpdateShowConfigWarnings(t *testing.T) {
	client, socketPath := startDaemon(t, `
[programs.web]
command = "/bin/sleep 300"
startsecs = 0
stopwaitsecs = 2
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)

	dir := filepath.Dir(socketPath)
	cfg := fmt.Sprintf(`[supervisor]
shutdown_timeout = 10
identifier = "host1"
nocleanup = true

[server.unix]
file = %q

[programs.web]
command = "/bin/sleep 300"
startsecs = 0
stopwaitsecs = 2
`, socketPath)
	if err := os.WriteFile(filepath.Join(dir, "kahi.toml"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	for _, sub := range []string{"reread", "update"} {
		stdout, stderr, code := runKahi(t, dir, "ctl", "-s", socketPath, sub)
		if code != 0 {
			t.Fatalf("ctl %s exited %d\nstdout: %s\nstderr: %s", sub, code, stdout, stderr)
		}
		for _, want := range []string{
			"warning: supervisor.identifier is not implemented yet and is ignored",
			"warning: supervisor.nocleanup is not implemented yet and is ignored",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("ctl %s stderr = %q, want it to contain %q", sub, stderr, want)
			}
		}
		if strings.Contains(stdout, "warning:") {
			t.Errorf("ctl %s printed warnings on stdout: %q", sub, stdout)
		}
	}
}
