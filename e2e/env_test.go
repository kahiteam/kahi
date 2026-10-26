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

func TestEnv_Passthrough(t *testing.T) {
	os.Setenv("KAHI_TEST_VAR", "passthrough-value")
	defer os.Unsetenv("KAHI_TEST_VAR")

	dir := t.TempDir()
	script := writeScript(t, dir, "envcheck.sh", "echo KAHI_TEST_VAR=$KAHI_TEST_VAR\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.envcheck]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "envcheck", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("envcheck", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !strings.Contains(buf.String(), "KAHI_TEST_VAR=passthrough-value") {
		t.Fatalf("env not passed through: %q", buf.String())
	}
}

func TestEnv_CleanEnvironment(t *testing.T) {
	os.Setenv("KAHI_CLEAN_TEST", "should-not-appear")
	defer os.Unsetenv("KAHI_CLEAN_TEST")

	dir := t.TempDir()
	script := writeScript(t, dir, "clean.sh", "echo CLEAN=$KAHI_CLEAN_TEST\necho CONFIGURED=$MY_VAR\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.clean]
command = "`+script+`"
autostart = true
startsecs = 0
clean_environment = true

[programs.clean.environment]
MY_VAR = "configured-value"
`)
	waitForState(t, client, "clean", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("clean", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	output := buf.String()
	if strings.Contains(output, "CLEAN=should-not-appear") {
		t.Fatalf("parent env leaked through clean_environment: %q", output)
	}
	if !strings.Contains(output, "CONFIGURED=configured-value") {
		t.Fatalf("configured env missing: %q", output)
	}
}

func TestEnv_ProgramOverridesGlobal(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "override.sh", "echo SHARED=$SHARED_VAR\nexec sleep 300")

	client, _ := startDaemon(t, `
[supervisor.environment]
SHARED_VAR = "global-value"

[programs.override]
command = "`+script+`"
autostart = true
startsecs = 0

[programs.override.environment]
SHARED_VAR = "program-value"
`)
	waitForState(t, client, "override", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("override", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !strings.Contains(buf.String(), "SHARED=program-value") {
		t.Fatalf("program env did not override global: %q", buf.String())
	}
}

func TestEnv_ProgramNameExpansion(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "expander.sh", "echo NAME=$PROC_NAME NUM=$PROC_NUM\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.expander]
command = "`+script+`"
process_name = "expander_%(process_num)d"
numprocs = 1
autostart = true
startsecs = 0

[programs.expander.environment]
PROC_NAME = "%(program_name)s"
PROC_NUM = "%(process_num)d"
`)
	waitForState(t, client, "expander_0", "RUNNING", 10*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("expander_0", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "NAME=expander") {
		t.Fatalf("%%(program_name)s not expanded: %q", output)
	}
	if !strings.Contains(output, "NUM=0") {
		t.Fatalf("%%(process_num)d not expanded: %q", output)
	}
}

func TestEnv_HereExpansion(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "here.sh", "echo HERE=$HERE_DIR\nexec sleep 300")

	client, socketPath := startDaemon(t, `
[programs.here]
command = "`+script+`"
autostart = true
startsecs = 0

[programs.here.environment]
HERE_DIR = "%(here)s"
`)
	waitForState(t, client, "here", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("here", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}

	configDir := filepath.Dir(socketPath)
	if !strings.Contains(buf.String(), "HERE="+configDir) {
		t.Fatalf("%%(here)s not expanded correctly: %q, want dir=%s", buf.String(), configDir)
	}
}

// A program defined in an included file gets %(here)s set to that file's
// directory, so the daemon finds a script that sits next to it (#109).
func TestEnv_HereExpansionInIncludedFile(t *testing.T) {
	dir, err := os.MkdirTemp("", "k-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	confDir := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, confDir, "run.sh", "echo started-from-include\nexec sleep 300")
	if err := os.WriteFile(filepath.Join(confDir, "web.toml"), []byte(`[programs.web]
command = "%(here)s/run.sh"
autostart = true
startsecs = 0
stopwaitsecs = 2
`), 0644); err != nil {
		t.Fatal(err)
	}

	socketPath := filepath.Join(dir, "kahi.sock")
	configPath := filepath.Join(dir, "kahi.toml")
	cfg := "include = [\"conf.d/*.toml\"]\n\n[supervisor]\nshutdown_timeout = 10\n\n[server.unix]\nfile = \"" + socketPath + "\"\n"
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := startDaemonCmd(t, kahiBinary, configPath, dir)
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitForSocket(t, socketPath, 5*time.Second)
	client := ctl.NewUnixClient(socketPath)
	waitForHealth(t, client, 3*time.Second)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)

	var buf bytes.Buffer
	if err := client.Tail("web", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !strings.Contains(buf.String(), "started-from-include") {
		t.Fatalf("stdout = %q, want started-from-include", buf.String())
	}
}

// With numprocs > 1, %(process_num)d in log paths and arguments is each
// instance's own number, so instances do not share a log file (#128).
func TestEnv_ProcessNumPerInstance(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatal(err)
	}
	script := writeScript(t, dir, "who.sh", "echo instance-$1\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.worker]
command = "`+script+` %(process_num)d"
process_name = "worker-%(process_num)d"
numprocs = 2
autostart = true
startsecs = 0
stopwaitsecs = 2
stdout_logfile = "`+logDir+`/worker-%(process_num)d.log"
`)
	for n := 0; n < 2; n++ {
		name := fmt.Sprintf("worker-%d", n)
		waitForState(t, client, name, "RUNNING", 5*time.Second)

		path := filepath.Join(logDir, name+".log")
		want := fmt.Sprintf("instance-%d", n)
		deadline := time.Now().Add(5 * time.Second)
		for {
			b, err := os.ReadFile(path)
			if err == nil && strings.TrimSpace(string(b)) == want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s = %q (err %v), want only %q", path, b, err, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}
