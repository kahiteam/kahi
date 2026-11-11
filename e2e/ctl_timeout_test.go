//go:build e2e

package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kahi ctl has no absolute limit on actions that wait on processes: the
// daemon bounds each stop by stopwaitsecs, and --timeout is opt-in (#222).

// stubbornPrograms returns n programs that ignore SIGTERM, so each stop runs
// the full stopwaitsecs before SIGKILL, and the ready markers they write.
func stubbornPrograms(t *testing.T, n, stopwaitsecs int) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	var cfg strings.Builder
	var ready []string
	for i := 0; i < n; i++ {
		marker := filepath.Join(dir, fmt.Sprintf("ready%d", i))
		script := writeScript(t, dir, fmt.Sprintf("stubborn%d.sh", i),
			"trap '' TERM\necho ready > "+marker+"\nwhile true; do sleep 0.1; done")
		fmt.Fprintf(&cfg, "[programs.s%d]\ncommand = %q\nstartsecs = 0\nstopwaitsecs = %d\n\n", i, script, stopwaitsecs)
		ready = append(ready, marker)
	}
	return cfg.String(), ready
}

// Two stops of 16 s each take longer than the old 30 s client limit.
func TestCtlTimeout_StopAllOutlasts30s(t *testing.T) {
	t.Parallel()
	cfg, ready := stubbornPrograms(t, 2, 16)
	client, socket := startDaemon(t, cfg)
	for i, marker := range ready {
		waitForState(t, client, fmt.Sprintf("s%d", i), "RUNNING", 5*time.Second)
		waitForMarker(t, marker, "ready", 5*time.Second)
	}

	start := time.Now()
	out, errOut, code := runKahiTimeout(t, 90*time.Second, "ctl", "-s", socket, "stop", "all")
	elapsed := time.Since(start)
	if code != 0 || out != "s0: ok\ns1: ok\n" && out != "s1: ok\ns0: ok\n" || errOut != "" {
		t.Fatalf("stop all: exit %d after %v, stdout %q, stderr %q; want both ok and exit 0", code, elapsed, out, errOut)
	}
	if elapsed < 30*time.Second {
		t.Errorf("stop all took %v; the test needs more than 30s to prove the old limit is gone", elapsed)
	}
}

// --timeout bounds the wait; the daemon keeps working on the action.
func TestCtlTimeout_Flag(t *testing.T) {
	t.Parallel()
	cfg, ready := stubbornPrograms(t, 1, 5)
	client, socket := startDaemon(t, cfg)
	waitForState(t, client, "s0", "RUNNING", 5*time.Second)
	waitForMarker(t, ready[0], "ready", 5*time.Second)

	start := time.Now()
	out, errOut, code := runKahi(t, t.TempDir(), "ctl", "-s", socket, "stop", "all", "--timeout", "1s")
	if code != 1 || out != "" || errOut != "daemon still working after 1s\n" {
		t.Errorf("stop all --timeout 1s: exit %d, stdout %q, stderr %q; want exit 1 and 'daemon still working after 1s'", code, out, errOut)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("gave up after %v, want about 1s", elapsed)
	}
	// The daemon finishes the stop: SIGKILL after stopwaitsecs.
	waitForState(t, client, "s0", "STOPPED", 10*time.Second)

	_, errOut, code = runKahi(t, t.TempDir(), "ctl", "-s", socket, "stop", "s0", "--timeout", "bogus")
	if code != 2 || errOut == "" {
		t.Errorf("--timeout bogus: exit %d, stderr %q; want a usage error, exit 2", code, errOut)
	}
}
