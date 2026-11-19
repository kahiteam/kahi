//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// kahi ctl -c dials the socket path the daemon expanded (%(here)s), and an
// environment variable's value reaches the program as it is: a $$ or %% in
// it is not expanded again (FUNC-012).
func TestConfig_ExpansionCtlSocketAndNoRecursion(t *testing.T) {
	dir := shortDir(t)
	out := filepath.Join(dir, "pw.out")
	script := writeScript(t, dir, "show.sh", "printf '%s' \"$PW\" > "+out+"\nexec /bin/sleep 300\n")
	cfgPath := filepath.Join(dir, "kahi.toml")
	body := fmt.Sprintf("[server.unix]\nfile = \"%%(here)s/kahi.sock\"\n\n[programs.w]\ncommand = %q\nstartsecs = 0\nenvironment = { PW = \"${ZZ_E2E_PW}\" }\n", script)
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZZ_E2E_PW", "a$$b%%c")
	cmd := startDaemonCmd(t, kahiBinary, cfgPath, dir)
	socket := filepath.Join(dir, "kahi.sock")
	t.Cleanup(func() { stopDaemon(t, cmd, socket) })
	waitForSocket(t, socket, 5*time.Second)
	waitForState(t, ctl.NewUnixClient(socket), "w", "RUNNING", 5*time.Second)

	stdout, stderr, code := runKahi(t, t.TempDir(), "ctl", "-c", cfgPath, "status", "w")
	if code != 0 || !strings.Contains(stdout, "RUNNING") {
		t.Errorf("ctl -c status: exit %d, %q %q", code, stdout, stderr)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(out)
		if string(b) == "a$$b%%c" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("PW %q, want a$$b%%%%c", b)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
