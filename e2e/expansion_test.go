//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
)

// These tests prove numprocs instances (FUNC-007) and config variable
// expansion (FUNC-012) against the kahi binary (#136).

// startDaemonInDir writes config to dir/kahi.toml with a control socket in
// dir, starts the daemon from another working directory and waits until it
// is healthy. Unlike startDaemon, the caller owns dir, so it can create
// files the config refers to before the daemon starts.
func startDaemonInDir(t *testing.T, dir, config string) *ctl.Client {
	t.Helper()
	socketPath := filepath.Join(dir, "kahi.sock")
	configPath := filepath.Join(dir, "kahi.toml")
	full := fmt.Sprintf("[supervisor]\nshutdown_timeout = 10\n\n[server.unix]\nfile = %q\n\n%s", socketPath, config)
	if err := os.WriteFile(configPath, []byte(full), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cmd := startDaemonCmd(t, kahiBinary, configPath, os.TempDir())
	t.Cleanup(func() {
		_ = ctl.NewUnixClient(socketPath).Shutdown()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	waitForSocket(t, socketPath, 5*time.Second)
	client := ctl.NewUnixClient(socketPath)
	waitForHealth(t, client, 3*time.Second)
	return client
}

// shortTempDir returns a temp dir short enough for a Unix socket path on
// macOS, removed when the test ends.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "k-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	// Resolve /var -> /private/var on macOS so paths match what children see.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// waitForOutput polls a process's stdout until it contains want and returns
// everything captured so far.
func waitForOutput(t *testing.T, client *ctl.Client, name, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var buf bytes.Buffer
		err := client.Tail(name, "stdout", 4096, &buf)
		if err == nil && strings.Contains(buf.String(), want) {
			return buf.String()
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s stdout = %q (err %v), want it to contain %q", name, buf.String(), err, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// pidsOf returns the PID of each named process, failing on a zero PID.
func pidsOf(t *testing.T, client *ctl.Client, names []string) map[string]int {
	t.Helper()
	pids := make(map[string]int, len(names))
	for _, name := range names {
		info, err := getProcessInfo(client, name)
		if err != nil {
			t.Fatalf("status %s: %v", name, err)
		}
		if info.PID <= 0 {
			t.Fatalf("%s has PID %d", name, info.PID)
		}
		pids[name] = info.PID
	}
	return pids
}

// numprocs_start offsets the instance numbers.
func TestNumprocs_StartOffset(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.worker]
command = "/bin/sleep 300"
process_name = "worker-%(process_num)d"
numprocs = 3
numprocs_start = 10
startsecs = 0
stopwaitsecs = 2
`)
	names := []string{"worker-10", "worker-11", "worker-12"}
	for _, name := range names {
		waitForState(t, client, name, "RUNNING", 10*time.Second)
	}
	infos, err := getAllProcessInfo(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != len(names) {
		t.Fatalf("got %d processes, want exactly %v: %+v", len(infos), names, infos)
	}
}

// Every instance is a separate child: unique names, unique non-zero PIDs,
// numbered sequentially from 0.
func TestNumprocs_FiveInstancesUniquePIDs(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.worker]
command = "/bin/sleep 300"
process_name = "worker-%(process_num)d"
numprocs = 5
startsecs = 0
stopwaitsecs = 2
`)
	var names []string
	for n := 0; n < 5; n++ {
		names = append(names, fmt.Sprintf("worker-%d", n))
	}
	for _, name := range names {
		waitForState(t, client, name, "RUNNING", 10*time.Second)
	}
	seen := make(map[int]string)
	for name, pid := range pidsOf(t, client, names) {
		if other, dup := seen[pid]; dup {
			t.Fatalf("%s and %s share PID %d", name, other, pid)
		}
		seen[pid] = name
	}
}

// Stopping or starting one instance leaves its siblings running untouched.
func TestNumprocs_InstancesIndependent(t *testing.T) {
	client, _ := startDaemon(t, `
[programs.worker]
command = "/bin/sleep 300"
process_name = "worker-%(process_num)d"
numprocs = 3
startsecs = 0
stopwaitsecs = 2
`)
	names := []string{"worker-0", "worker-1", "worker-2"}
	for _, name := range names {
		waitForState(t, client, name, "RUNNING", 10*time.Second)
	}
	before := pidsOf(t, client, names)

	if err := client.Stop("worker-1"); err != nil {
		t.Fatalf("stop worker-1: %v", err)
	}
	waitForState(t, client, "worker-1", "STOPPED", 5*time.Second)
	siblings := []string{"worker-0", "worker-2"}
	for name, pid := range pidsOf(t, client, siblings) {
		if info, _ := getProcessInfo(client, name); info.State != "RUNNING" || pid != before[name] {
			t.Fatalf("after stopping worker-1, %s is %s with PID %d, want RUNNING with PID %d", name, info.State, pid, before[name])
		}
	}

	if err := client.Start("worker-1"); err != nil {
		t.Fatalf("start worker-1: %v", err)
	}
	waitForState(t, client, "worker-1", "RUNNING", 5*time.Second)
	after := pidsOf(t, client, names)
	if after["worker-1"] == before["worker-1"] {
		t.Fatalf("worker-1 kept PID %d after stop and start", before["worker-1"])
	}
	for _, name := range siblings {
		if after[name] != before[name] {
			t.Fatalf("%s PID changed from %d to %d", name, before[name], after[name])
		}
	}
}

// All four template variables expand per instance in environment values.
// %(group_name)s is the program's own group; see #167 for programs listed in
// an explicit [groups] table.
func TestNumprocs_TemplateVariables(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "vars.sh", "echo VARS=$P,$N,$G,$C\nexec sleep 300")
	client, _ := startDaemon(t, `
[programs.worker]
command = "`+script+`"
process_name = "worker-%(process_num)d"
numprocs = 2
startsecs = 0
stopwaitsecs = 2

[programs.worker.environment]
P = "%(program_name)s"
N = "%(process_num)d"
G = "%(group_name)s"
C = "%(numprocs)d"
`)
	for n := 0; n < 2; n++ {
		name := fmt.Sprintf("worker-%d", n)
		waitForState(t, client, name, "RUNNING", 10*time.Second)
		waitForOutput(t, client, name, fmt.Sprintf("VARS=worker,%d,worker,2", n))
	}
}

// Variables expand in command, directory, log paths and environment, with
// %% and $$ escapes, no recursive expansion, and several references in one
// value. Values are expanded once, at load: a restart sees the same values.
func TestExpansion_ConfigValues(t *testing.T) {
	dir := shortTempDir(t)
	binDir := filepath.Join(dir, "bin")
	for _, d := range []string{binDir, filepath.Join(dir, "data"), filepath.Join(dir, "logs")} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeScript(t, binDir, "show.sh", "echo \"PWD=$(pwd)\"\necho \"ESC=$ESC\"\necho \"LIT=$LIT\"\necho \"MULTI=$MULTI\"\nexec sleep 300")
	t.Setenv("KAHI_E2E_BIN", binDir)
	t.Setenv("KAHI_E2E_X", "xval")

	client := startDaemonInDir(t, dir, `
[programs.show]
command = "${KAHI_E2E_BIN}/show.sh"
directory = "%(here)s/data"
process_name = "show-%(process_num)d"
numprocs = 2
startsecs = 0
stopwaitsecs = 2
stdout_logfile = "%(here)s/logs/%(program_name)s-%(process_num)d.log"

[programs.show.environment]
ESC = "100%% of $$HOME"
LIT = "%%(program_name)s"
MULTI = "%(program_name)s-%(process_num)d-${KAHI_E2E_X}"
`)
	for n := 0; n < 2; n++ {
		name := fmt.Sprintf("show-%d", n)
		waitForState(t, client, name, "RUNNING", 10*time.Second)
		out := waitForOutput(t, client, name, "MULTI=")
		for _, want := range []string{
			"PWD=" + filepath.Join(dir, "data"),
			"ESC=100% of $HOME",
			"LIT=%(program_name)s",
			fmt.Sprintf("MULTI=show-%d-xval", n),
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s stdout = %q, want it to contain %q", name, out, want)
			}
		}
		logPath := filepath.Join(dir, "logs", fmt.Sprintf("show-%d.log", n))
		if b, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(b), "MULTI=") {
			t.Errorf("%s = %q (err %v), want the instance's output", logPath, b, err)
		}
	}

	// The daemon's environment cannot change after it starts, so the value
	// read at load is what every later start of the process sees.
	if err := client.Restart("show-1"); err != nil {
		t.Fatalf("restart show-1: %v", err)
	}
	waitForState(t, client, "show-1", "RUNNING", 5*time.Second)
	// The capture buffer keeps both starts' output; wait for the second.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var buf bytes.Buffer
		_ = client.Tail("show-1", "stdout", 4096, &buf)
		if strings.Count(buf.String(), "MULTI=show-1-xval") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after restart show-1 stdout = %q, want MULTI=show-1-xval from both starts", buf.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// An expansion error is a config error: the daemon does not start.
func TestExpansion_Errors(t *testing.T) {
	for _, tc := range []struct {
		name, program, want string
	}{
		{
			name:    "undefined environment variable",
			program: "command = \"${KAHI_E2E_UNDEFINED}/server\"",
			want:    "undefined environment variable: ${KAHI_E2E_UNDEFINED}",
		},
		{
			name:    "unknown template variable",
			program: "command = \"/bin/sleep 300\"\n\n[programs.web.environment]\nX = \"%(foo)s\"",
			want:    "unknown template variable: %(foo)s",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := shortTempDir(t)
			configPath := filepath.Join(dir, "kahi.toml")
			cfg := fmt.Sprintf("[server.unix]\nfile = %q\n\n[programs.web]\n%s\n", filepath.Join(dir, "kahi.sock"), tc.program)
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
			if _, err := os.Stat(filepath.Join(dir, "kahi.sock")); err == nil {
				t.Fatal("control socket exists; the daemon got as far as serving")
			}
		})
	}
}
