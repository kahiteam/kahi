//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// kahi migrate fixes from the v0.3.0 review: a dotted program name, a
// quoted comma in an environment value and a literal $ survive migration,
// and the daemon runs the result with them.
func TestMigrate_NamesEnvironmentAndDollar(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	socket := filepath.Join(dir, "kahi.sock")
	out := filepath.Join(dir, "env.out")
	script := writeScript(t, dir, "show.sh", "printf '%s|%s\\n' \"$OPTS\" \"$1\" > "+out+"\nexec /bin/sleep 300\n")
	in := "[unix_http_server]\nfile=" + socket + "\n\n" +
		"[program:api]\ncommand=/bin/sleep 300\nstartsecs=0\n\n" +
		"[program:api.worker]\ncommand=" + script + " cost$5\nstartsecs=0\nenvironment=OPTS=\"-Xms1g,-Xmx2g\",B=z\nstdout_logfile=NONE\n"
	if err := os.WriteFile(filepath.Join(dir, "in.conf"), []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if o, e, code := runKahi(t, dir, "migrate", "in.conf", "-o", "out.toml"); code != 0 {
		t.Fatalf("migrate: exit %d, %q %q", code, o, e)
	}
	cmd := startDaemonCmd(t, kahiBinary, filepath.Join(dir, "out.toml"), dir)
	t.Cleanup(func() { stopDaemon(t, cmd, socket) })
	waitForSocket(t, socket, 5*time.Second)
	client := ctl.NewUnixClient(socket)
	waitForState(t, client, "api", "RUNNING", 5*time.Second)
	waitForState(t, client, "api.worker", "RUNNING", 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(out)
		if got := strings.TrimSpace(string(b)); got == "-Xms1g,-Xmx2g|cost$5" {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("api.worker saw %q, want -Xms1g,-Xmx2g|cost$5", got)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
