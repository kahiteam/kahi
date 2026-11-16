//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/testutil"
)

// waitForLog polls a log read until ok accepts the body or 5 s pass, and
// returns the last response.
func waitForLog(t *testing.T, socket, name, stream, length string, ok func([]byte) bool) apiResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := apiDo(t, socket, "GET", "/api/v1/processes/"+name+"/log/"+stream+"?length="+length, "")
		if (r.status == http.StatusOK && ok(r.body)) || time.Now().After(deadline) {
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// numberedLines prints count lines "line NNNN" (10 bytes each) to fd.
func numberedLines(count int, fd string) string {
	return fmt.Sprintf("i=0\nwhile [ $i -lt %d ]; do printf 'line %%04d\\n' $i >&%s; i=$((i+1)); done\n", count, fd)
}

// FUNC-086 / ADR-048: the buffer size is per program and stream; a length
// above the buffer returns the whole buffer, one above 16777216 is refused.
// The Web UI's fixed 65536-byte request works with a smaller buffer.
func TestCaptureBuffer_SizeAndCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	big := writeScript(t, dir, "big.sh", "head -c 2097152 /dev/zero | tr '\\000' x\nprintf END\nexec sleep 300")
	small := writeScript(t, dir, "small.sh", numberedLines(1100, "1")+numberedLines(1100, "2")+"exec sleep 300")
	tiny := writeScript(t, dir, "tiny.sh", "echo hi\nexec sleep 300")
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreeTCPPort(t))
	client, socket := startDaemon(t, fmt.Sprintf(`[server.http]
enabled = true
listen = %q
username = "admin"
%s = %q

[server.web]
enabled = true

[programs.big]
command = %q
startsecs = 0
stdout_capture_maxbytes = "1MB"

[programs.small]
command = %q
startsecs = 0
stdout_capture_maxbytes = "4KB"
stderr_capture_maxbytes = "4KB"

[programs.tiny]
command = %q
startsecs = 0
stdout_capture_maxbytes = "4KB"
`, addr, "pass"+"word", hashPassword(t, "secret"), big, small, tiny))
	for _, p := range []string{"big", "small", "tiny"} {
		waitForState(t, client, p, "RUNNING", 5*time.Second)
	}

	// 1MB buffer after 2 MB of output: the last 1048576 bytes.
	r := waitForLog(t, socket, "big", "stdout", "1048576", func(b []byte) bool {
		return len(b) == 1048576 && strings.HasSuffix(string(b), "END")
	})
	if len(r.body) != 1048576 || !strings.HasSuffix(string(r.body), "xEND") {
		t.Errorf("big: %d bytes, want 1048576 ending in xEND", len(r.body))
	}
	// Longer than the buffer, up to the cap: the whole buffer.
	if r := waitForLog(t, socket, "big", "stdout", "16777216", func([]byte) bool { return true }); r.status != http.StatusOK || len(r.body) != 1048576 {
		t.Errorf("big length=16777216: %d, %d bytes; want 200 with 1048576", r.status, len(r.body))
	}
	// Above the cap: refused before any allocation (api-input-bounds).
	for _, length := range []string{"16777217", "999999999"} {
		apiDo(t, socket, "GET", "/api/v1/processes/big/log/stdout?length="+length, "").
			wantError(t, "length "+length, http.StatusBadRequest, "length exceeds maximum of 16777216", "BAD_REQUEST")
	}
	// No length, or a negative one, reads the default 1600 bytes.
	for _, q := range []string{"", "?length=-5"} {
		r := apiDo(t, socket, "GET", "/api/v1/processes/big/log/stdout"+q, "")
		if r.status != http.StatusOK || len(r.body) != 1600 || !strings.HasSuffix(string(r.body), "xEND") {
			t.Errorf("big%s: %d, %d bytes; want 200 with the last 1600", q, r.status, len(r.body))
		}
	}

	// 4KB buffers after 11000 bytes per stream: a 65536-byte read (what the
	// Web UI log viewer requests) returns the whole 4096-byte buffer.
	for _, stream := range []string{"stdout", "stderr"} {
		r := waitForLog(t, socket, "small", stream, "65536", func(b []byte) bool {
			return strings.HasSuffix(string(b), "line 1099\n")
		})
		if r.status != http.StatusOK || len(r.body) != 4096 || !strings.HasSuffix(string(r.body), "line 1099\n") {
			t.Errorf("small %s: %d, %d bytes; want 200 with 4096 ending in line 1099", stream, r.status, len(r.body))
		}
		if strings.Contains(string(r.body), "line 0000") {
			t.Errorf("small %s: buffer still holds the first line", stream)
		}
		// The bytes start mid-output, so the viewer drops the first line.
		if h := r.header.Get("X-Kahi-Log-Truncated"); h != "true" {
			t.Errorf("small %s: X-Kahi-Log-Truncated = %q, want true", stream, h)
		}
	}
	// Everything the program wrote: the first line is whole, no header.
	r = waitForLog(t, socket, "tiny", "stdout", "65536", func(b []byte) bool { return string(b) == "hi\n" })
	if string(r.body) != "hi\n" || r.header.Get("X-Kahi-Log-Truncated") != "" {
		t.Errorf("tiny: %q, X-Kahi-Log-Truncated %q; want hi and no header", r.body, r.header.Get("X-Kahi-Log-Truncated"))
	}
	page := apiDo(t, socket, "GET", "/ui/log/small/stdout", "")
	if page.status != http.StatusOK {
		t.Errorf("GET /ui/log/small/stdout: %d, want 200", page.status)
	}
}

// A size outside 1KB..16MB, or one that does not parse, refuses to start.
func TestCaptureBuffer_InvalidSizeRefused(t *testing.T) {
	t.Parallel()
	for size, want := range map[string]string{
		"100":  "programs.p.stdout_capture_maxbytes: must be between 1KB and 16MB",
		"32MB": "programs.p.stdout_capture_maxbytes: must be between 1KB and 16MB",
		"abc":  "programs.p.stdout_capture_maxbytes: invalid size abc",
	} {
		expectRefused(t, "[programs.p]\ncommand = \"/bin/sleep 300\"\nstdout_capture_maxbytes = \""+size+"\"\n", want)
	}
}

// A size change on reload restarts the program with a new, empty buffer.
func TestCaptureBuffer_ReloadRecreates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The program prints only after a line on stdin, so an empty read after
	// the restart is not a race with its output.
	p := writeScript(t, dir, "p.sh", "read l\nhead -c 10240 /dev/zero | tr '\\000' z\nexec sleep 300")
	prog := func(size string) string {
		return "[programs.p]\ncommand = \"" + p + "\"\nstartsecs = 0\nstopwaitsecs = 2\nstdout_capture_maxbytes = \"" + size + "\"\n"
	}
	client, socket := startDaemon(t, prog("64KB"))
	waitForState(t, client, "p", "RUNNING", 5*time.Second)
	sendStdin(t, socket, "p", "go\n")
	if r := waitForLog(t, socket, "p", "stdout", "65536", func(b []byte) bool { return len(b) == 10240 }); len(r.body) != 10240 {
		t.Fatalf("before reload: %d bytes, want 10240", len(r.body))
	}
	before, err := getProcessInfo(client, "p")
	if err != nil {
		t.Fatal(err)
	}

	rewriteConfig(t, socket, prog("32KB"))
	if _, err := client.Reload(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var after processInfo
	for {
		after, err = getProcessInfo(client, "p")
		if err == nil && after.State == "RUNNING" && after.PID != 0 && after.PID != before.PID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after reload: %+v (err %v), want RUNNING with a new pid (old %d)", after, err, before.PID)
		}
		time.Sleep(50 * time.Millisecond)
	}
	r := apiDo(t, socket, "GET", "/api/v1/processes/p/log/stdout?length=65536", "")
	if r.status != http.StatusOK || len(r.body) != 0 {
		t.Errorf("after reload: %d, %d bytes; want 200 with an empty buffer", r.status, len(r.body))
	}
}

// With redirect_stderr there is no stderr buffer to size.
func TestCaptureBuffer_RedirectWarning(t *testing.T) {
	t.Parallel()
	client, _, out := startDaemonOut(t, `
[programs.p]
command = "/bin/sleep 300"
startsecs = 0
redirect_stderr = true
stderr_capture_maxbytes = "4KB"
`)
	waitForState(t, client, "p", "RUNNING", 5*time.Second)
	b, _ := os.ReadFile(out.Name())
	if !strings.Contains(string(b), "programs.p.stderr_capture_maxbytes has no effect with redirect_stderr") {
		t.Errorf("no redirect warning in daemon output:\n%s", b)
	}
	if strings.Contains(string(b), "not implemented yet") {
		t.Errorf("ignored-setting warning in daemon output:\n%s", b)
	}
}

// supervisord's key selects a capture mode for PROCESS_COMMUNICATION events,
// not a buffer size, so migrate does not carry it over.
func TestCaptureBuffer_MigrateKeepsComment(t *testing.T) {
	t.Parallel()
	out, _, code := migrate(t, "[program:web]\ncommand=/bin/sleep 300\nstdout_capture_maxbytes=1MB\n")
	if code != 0 {
		t.Fatalf("migrate exit %d", code)
	}
	want := "# UNSUPPORTED: stdout_capture_maxbytes = 1MB (supervisord capture mode for PROCESS_COMMUNICATION events, not a buffer size)"
	if !strings.Contains(out, want) {
		t.Errorf("migrate output lacks %q:\n%s", want, out)
	}
}
