//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// kahi ctl tail, run as the binary (FUNC-035). No program here has a log
// file, so every read comes from the in-memory buffer.
func TestCtl_Tail(t *testing.T) {
	dir := t.TempDir()
	// 3000 bytes of stdout (300 lines of 9 digits and a newline), then one
	// line on stderr.
	script := writeScript(t, dir, "talk.sh", `i=0
while [ $i -lt 300 ]; do printf '%09d\n' $i; i=$((i+1)); done
echo on-stderr >&2
exec sleep 300`)
	client, socket := startDaemon(t, `
[programs.web]
command = "`+script+`"
startsecs = 0
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	waitForOutput(t, client, "web", "000000299")
	ctl := func(args ...string) (string, string, int) {
		return runKahi(t, dir, append([]string{"ctl", "-s", socket}, args...)...)
	}

	// The last 1600 bytes by default, --bytes otherwise.
	out, _, code := ctl("tail", "web")
	if code != 0 || len(out) != 1600 || !strings.HasSuffix(out, "000000299\n") {
		t.Errorf("tail web: exit %d, %d bytes, ends %q", code, len(out), out[max(0, len(out)-20):])
	}
	out, _, code = ctl("tail", "--bytes", "500", "web")
	if code != 0 || len(out) != 500 || !strings.HasSuffix(out, "000000299\n") {
		t.Errorf("tail --bytes 500 web: exit %d, %d bytes", code, len(out))
	}
	out, _, code = ctl("tail", "web", "stderr")
	if code != 0 || out != "on-stderr\n" {
		t.Errorf("tail web stderr: exit %d, %q", code, out)
	}
	_, stderr, code := ctl("tail", "nonexistent")
	if code != 1 || !strings.Contains(stderr, "no such process: nonexistent") {
		t.Errorf("tail nonexistent: exit %d, stderr %q", code, stderr)
	}

	// tail -f ends cleanly on Ctrl+C.
	cmd := exec.Command(kahiBinary, "ctl", "-s", socket, "tail", "-f", "web")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("tail -f after Ctrl+C: %v, want exit 0", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("tail -f still running 5s after Ctrl+C")
	}
}
