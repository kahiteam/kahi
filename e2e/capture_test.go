//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Process output capture (FUNC-018) and the in-memory ring buffer (FUNC-082).

// jsonLines returns the daemon output lines of process as decoded objects.
func jsonLines(t *testing.T, out, process string) []map[string]string {
	t.Helper()
	var lines []map[string]string
	for _, l := range strings.Split(out, "\n") {
		var m map[string]string
		if json.Unmarshal([]byte(l), &m) == nil && m["process"] == process {
			lines = append(lines, m)
		}
	}
	return lines
}

func TestCapture_LinesAndFiles(t *testing.T) {
	t.Parallel()
	dir := shortTempDir(t)
	writeScript(t, dir, "hello.sh", "echo hello")
	writeScript(t, dir, "err.sh", "echo err >&2")
	writeScript(t, dir, "chunks.sh", "printf 'a\\nb\\nc'\nsleep 1\nprintf '\\n'")
	writeScript(t, dir, "final.sh", "printf 'tail'")
	stdout, _ := startDaemonCaptured(t, dir, `
[programs.hello]
command = "DIR/hello.sh"
autorestart = "false"
startsecs = 0

[programs.tofile]
command = "DIR/hello.sh"
autorestart = "false"
startsecs = 0
stdout_logfile = "DIR/tofile.log"

[programs.merged]
command = "DIR/err.sh"
autorestart = "false"
startsecs = 0
redirect_stderr = true

[programs.mergedfile]
command = "DIR/err.sh"
autorestart = "false"
startsecs = 0
redirect_stderr = true
stdout_logfile = "DIR/merged.log"

[programs.chunks]
command = "DIR/chunks.sh"
autorestart = "false"
startsecs = 0

[programs.final]
command = "DIR/final.sh"
autorestart = "false"
startsecs = 0
`)

	// One JSON line per output line, with time, process, stream and log.
	hello := jsonLines(t, stdout, "hello")
	if len(hello) != 1 || hello[0]["stream"] != "stdout" || hello[0]["log"] != "hello" || hello[0]["time"] == "" {
		t.Errorf("hello lines %v, want one stdout line 'hello' with a time", hello)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "tofile.log")); err != nil || string(b) != "hello\n" {
		t.Errorf("tofile.log = %q (err %v), want exactly hello", b, err)
	}
	if m := jsonLines(t, stdout, "merged"); len(m) != 1 || m[0]["stream"] != "stdout" || m[0]["log"] != "err" {
		t.Errorf("merged lines %v, want stderr on the stdout stream", m)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "merged.log")); err != nil || string(b) != "err\n" {
		t.Errorf("merged.log = %q (err %v), want the stderr line", b, err)
	}

	// Two lines in one chunk, then a line completed a second later: three
	// lines, the third stamped when its newline arrived.
	chunks := jsonLines(t, stdout, "chunks")
	if len(chunks) != 3 || chunks[0]["log"] != "a" || chunks[1]["log"] != "b" || chunks[2]["log"] != "c" {
		t.Fatalf("chunks lines %v, want a, b, c", chunks)
	}
	tb, _ := time.Parse(time.RFC3339Nano, chunks[1]["time"])
	tc, _ := time.Parse(time.RFC3339Nano, chunks[2]["time"])
	if d := tc.Sub(tb); d < 800*time.Millisecond {
		t.Errorf("c was emitted %v after b, want it only after its newline (~1s)", d)
	}
	// A last line without a newline is flushed when the process exits.
	if f := jsonLines(t, stdout, "final"); len(f) != 1 || f[0]["log"] != "tail" {
		t.Errorf("final lines %v, want 'tail'", f)
	}
}

// A child writing 10 MB without a newline is not blocked by the capture.
func TestCapture_LargeBurstDoesNotBlock(t *testing.T) {
	t.Parallel()
	script := writeScript(t, t.TempDir(), "burst.sh", "head -c 10485760 /dev/zero | tr '\\000' a")
	client, _ := startDaemon(t, `
[programs.burst]
command = "`+script+`"
startsecs = 0
autorestart = "false"
`)
	waitForState(t, client, "burst", "EXITED", 5*time.Second)
}

// SEC-012: a log file path that is a symlink is not followed; the start
// fails and the target is untouched.
func TestCapture_SymlinkLogfileRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "out.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	client, _ := startDaemon(t, `
[programs.web]
command = "/bin/echo hello"
startsecs = 0
startretries = 0
stdout_logfile = "`+link+`"
`)
	waitForState(t, client, "web", "FATAL", 5*time.Second)
	if info, _ := getProcessInfo(client, "web"); !strings.Contains(info.Description, "cannot open log file") {
		t.Errorf("description %q, want 'cannot open log file'", info.Description)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep\n" {
		t.Errorf("symlink target = %q, want it untouched", b)
	}
}

func TestRingBuffer_ReadsAndEviction(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	evict := writeScript(t, dir, "evict.sh", "head -c 65536 /dev/zero | tr '\\000' a\nprintf bbbbbbbbbb\nexec sleep 300")
	big := writeScript(t, dir, "big.sh", "head -c 70000 /dev/zero | tr '\\000' x\nexec sleep 300")
	both := writeScript(t, dir, "both.sh", "echo out\necho err >&2\nexec sleep 300")
	client, socket := startDaemon(t, `
[programs.evict]
command = "`+evict+`"
startsecs = 0

[programs.big]
command = "`+big+`"
startsecs = 0

[programs.both]
command = "`+both+`"
startsecs = 0
`)
	read := func(name, stream, length string) apiResponse {
		return apiDo(t, socket, "GET", "/api/v1/processes/"+name+"/log/"+stream+"?length="+length, "")
	}
	// waitFor polls until the buffer is full and ends with suffix: a full
	// buffer alone can be read before the script's last write lands.
	waitFor := func(name, suffix string) []byte {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			r := read(name, "stdout", "65536")
			if (len(r.body) == 65536 && strings.HasSuffix(string(r.body), suffix)) || time.Now().After(deadline) {
				return r.body
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	for _, p := range []string{"evict", "big", "both"} {
		waitForState(t, client, p, "RUNNING", 5*time.Second)
	}

	// 65546 bytes written: the buffer holds the last 65536, ending in b.
	if b := waitFor("evict", "abbbbbbbbbb"); len(b) != 65536 || !strings.HasSuffix(string(b), "abbbbbbbbbb") {
		t.Errorf("evict buffer %d bytes, ends %q, want 65536 ending in 10 b", len(b), string(b[max(0, len(b)-12):]))
	}
	// 70000 bytes written: a read returns exactly the last 65536; a longer
	// read is refused (api-input-bounds).
	if b := waitFor("big", "x"); len(b) != 65536 || strings.Trim(string(b), "x") != "" {
		t.Errorf("big buffer %d bytes, want 65536 x", len(b))
	}
	read("big", "stdout", "1000000").wantError(t, "length 1000000", http.StatusBadRequest, "length exceeds maximum of 65536", "BAD_REQUEST")

	// Each stream has its own buffer.
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, errOut := read("both", "stdout", "100"), read("both", "stderr", "100")
		if string(out.body) == "out\n" && string(errOut.body) == "err\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stdout %q, stderr %q, want out and err", out.body, errOut.body)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Removed by a reload, the program's buffers go with it.
	rewriteConfig(t, socket, "[programs.evict]\ncommand = \""+evict+"\"\nstartsecs = 0\n\n[programs.big]\ncommand = \""+big+"\"\nstartsecs = 0\n")
	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	read("both", "stdout", "100").wantError(t, "removed both", http.StatusNotFound, "no such process: both", "NOT_FOUND")
}
