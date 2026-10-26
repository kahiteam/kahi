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

// Configs whose processes would share a name are refused: the daemon tracks
// processes by name, so all but one would silently never run (#166).
func TestNumprocs_ConfigErrors(t *testing.T) {
	for _, tc := range []struct {
		name, programs, want string
	}{
		{
			name:     "numprocs with a fixed process_name",
			programs: "[programs.w]\ncommand = \"/bin/sleep 300\"\nnumprocs = 2\nprocess_name = \"w\"\n",
			want:     `programs.w: process_name "w" must contain %(process_num)d when numprocs > 1`,
		},
		{
			name:     "two programs with the same process_name",
			programs: "[programs.a]\ncommand = \"/bin/sleep 300\"\nprocess_name = \"x\"\n\n[programs.b]\ncommand = \"/bin/sleep 300\"\nprocess_name = \"x\"\n",
			want:     "duplicate process name: x (programs.a and programs.b)",
		},
		{
			name:     "negative numprocs",
			programs: "[programs.w]\ncommand = \"/bin/sleep 300\"\nnumprocs = -1\nprocess_name = \"w-%(process_num)d\"\n",
			want:     "programs.w: numprocs must be >= 1, got -1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "k-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			configPath := filepath.Join(dir, "kahi.toml")
			cfg := fmt.Sprintf("[server.unix]\nfile = %q\n\n%s", filepath.Join(dir, "kahi.sock"), tc.programs)
			if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := runKahi(t, dir, "daemon", "-c", configPath)
			if code == 0 {
				t.Fatalf("daemon exited 0, want a config error\nstdout: %s\nstderr: %s", stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr, tc.want)
			}
		})
	}
}

// A reload that would create a duplicate process name is rejected, and the
// running processes are left alone.
func TestReload_DuplicateProcessNameRejected(t *testing.T) {
	client, socketPath := startDaemon(t, `
[programs.a]
command = "/bin/sleep 300"
startsecs = 0
stopwaitsecs = 2
`)
	waitForState(t, client, "a", "RUNNING", 5*time.Second)
	before, err := getProcessInfo(client, "a")
	if err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(filepath.Dir(socketPath), "kahi.toml")
	cfg := fmt.Sprintf(`[supervisor]
shutdown_timeout = 10

[server.unix]
file = %q

[programs.a]
command = "/bin/sleep 300"
startsecs = 0
stopwaitsecs = 2

[programs.b]
command = "/bin/sleep 301"
process_name = "a"
startsecs = 0
`, socketPath)
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Reload(); err == nil || !strings.Contains(err.Error(), "duplicate process name: a") {
		t.Fatalf("reload err = %v, want duplicate process name: a", err)
	}

	after, err := getProcessInfo(client, "a")
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "RUNNING" || after.PID != before.PID {
		t.Fatalf("after rejected reload a is %s with PID %d, want RUNNING with PID %d", after.State, after.PID, before.PID)
	}
	infos, err := getAllProcessInfo(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("got %d processes after rejected reload, want 1: %+v", len(infos), infos)
	}
}
