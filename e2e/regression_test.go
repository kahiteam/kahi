//go:build e2e

package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRegression_UnicodeTail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "unicode.sh", "printf 'Hello \\xe4\\xb8\\x96\\xe7\\x95\\x8c\\n'\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.unicode]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "unicode", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("unicode", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !strings.Contains(buf.String(), "Hello") {
		t.Fatalf("unicode tail output missing content: %q", buf.String())
	}
}

func TestRegression_InvalidUTF8(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "badutf8.sh", "printf '\\xff\\xfe invalid bytes\\n'\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.badutf8]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "badutf8", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("badutf8", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !strings.Contains(buf.String(), "invalid bytes") {
		t.Fatalf("invalid UTF-8 output: %q", buf.String())
	}
}

func TestRegression_LiteralPercent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "percent.sh", "echo '100% complete'\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.percent]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "percent", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("percent", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !strings.Contains(buf.String(), "100%") {
		t.Fatalf("literal percent not preserved: %q", buf.String())
	}
}

func TestRegression_KahiInit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cmd := exec.Command(kahiBinary, "init")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kahi init failed: %v\n%s", err, out)
	}

	configPath := dir + "/kahi.toml"
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("kahi.toml not created: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("kahi.toml is empty")
	}
	if !strings.Contains(string(data), "[supervisor]") {
		t.Fatalf("generated config missing [supervisor]: %s", string(data))
	}
}

func TestRegression_HelpFlag(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(kahiBinary, "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kahi --help failed: %v\n%s", err, out)
	}
	output := string(out)
	if !strings.Contains(output, "daemon") {
		t.Fatalf("help output missing 'daemon': %s", output)
	}
	if !strings.Contains(output, "ctl") {
		t.Fatalf("help output missing 'ctl': %s", output)
	}
}

func TestRegression_PipedTail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "piper.sh", "for i in 1 2 3 4 5; do echo pipe-line-$i; done\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.piper]
command = "`+script+`"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "piper", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("piper", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail: %v", err)
	}
	output := buf.String()
	for _, expected := range []string{"pipe-line-1", "pipe-line-5"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("piped output missing %q: %q", expected, output)
		}
	}
}

func TestRegression_NumprocsNames(t *testing.T) {
	t.Parallel()
	client, _ := startDaemon(t, `
[programs.multi]
command = "/bin/sleep 300"
process_name = "multi_%(process_num)d"
numprocs = 3
numprocs_start = 0
autostart = true
startsecs = 0
`)
	// Verify naming convention: multi_0, multi_1, multi_2.
	for _, name := range []string{"multi_0", "multi_1", "multi_2"} {
		waitForState(t, client, name, "RUNNING", 10*time.Second)
	}
}

func TestRegression_RedirectStderr(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "merged.sh", "echo stdout-line\necho stderr-line >&2\nexec sleep 300")

	client, _ := startDaemon(t, `
[programs.merged]
command = "`+script+`"
autostart = true
startsecs = 0
redirect_stderr = true
`)
	waitForState(t, client, "merged", "RUNNING", 5*time.Second)
	time.Sleep(1 * time.Second)

	var buf bytes.Buffer
	if err := client.Tail("merged", "stdout", 4096, &buf); err != nil {
		t.Fatalf("tail stdout: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "stdout-line") {
		t.Fatalf("stdout missing: %q", output)
	}
	if !strings.Contains(output, "stderr-line") {
		t.Fatalf("stderr not redirected to stdout: %q", output)
	}
}
