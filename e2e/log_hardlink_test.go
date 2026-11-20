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

// SEC-019 (decided 2026-11-20): a log path that is a hard link to another
// file is refused, so a root daemon never appends program output to a file
// a program user linked into its log directory. The test user links a file
// it owns, which every platform allows.

// linkedLog creates dir/protected and makes dir/name a second link to it.
func linkedLog(t *testing.T, dir, name string) (target, log string) {
	t.Helper()
	target = filepath.Join(dir, "protected")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	log = filepath.Join(dir, name)
	if err := os.Link(target, log); err != nil {
		t.Fatal(err)
	}
	return target, log
}

func TestLog_HardLinkedLogfileRefused(t *testing.T) {
	t.Parallel()
	target, log := linkedLog(t, t.TempDir(), "out.log")
	client, _ := startDaemon(t, `
[programs.web]
command = "/bin/echo hello"
startsecs = 0
startretries = 0
stdout_logfile = "`+log+`"
`)
	waitForState(t, client, "web", "FATAL", 5*time.Second)
	info, _ := getProcessInfo(client, "web")
	if want := "cannot open log file for web: " + log + ": "; !strings.HasPrefix(info.Description, want) ||
		!strings.Contains(info.Description, "2 hard links; refusing to write a log file linked elsewhere") {
		t.Errorf("description %q, want %q... and the hard-link reason", info.Description, want)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep\n" {
		t.Errorf("linked target = %q, want it untouched", b)
	}
}

func TestDaemonLog_HardLinkRefused(t *testing.T) {
	t.Parallel()
	dir := shortTempDir(t)
	target, log := linkedLog(t, dir, "kahi.log")
	cfg := filepath.Join(dir, "kahi.toml")
	writeLogConfig(t, cfg, filepath.Join(dir, "kahi.sock"), fmt.Sprintf("logfile = %q\n", log), "")

	t.Run("daemon", func(t *testing.T) {
		_, stderr, code := runKahi(t, dir, "daemon", "-c", cfg)
		if code == 0 || !strings.Contains(stderr, "cannot open log file: "+log+": ") || !strings.Contains(stderr, "2 hard links") {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
	})
	t.Run("config check", func(t *testing.T) {
		_, stderr, code := runKahi(t, dir, "config", "check", "-c", cfg)
		if code == 0 || !strings.Contains(stderr, "error: cannot open log file: "+log+": ") || !strings.Contains(stderr, "2 hard links") {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
	})
	if b, _ := os.ReadFile(target); string(b) != "keep\n" {
		t.Errorf("linked target = %q, want it untouched", b)
	}
}
