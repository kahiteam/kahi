//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A stdin write to a program that never reads stdin fails after the stall
// with 409 instead of hanging the process lock: status answers meanwhile and
// the write gives up (FUNC-077).
func TestStdin_FullPipeDoesNotHangDaemon(t *testing.T) {
	t.Parallel()
	client, socket, _ := startDaemonOut(t, "[programs.sleeper]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\n")
	waitForState(t, client, "sleeper", "RUNNING", 5*time.Second)

	body, _ := json.Marshal(map[string]string{"data": strings.Repeat("x", 900<<10)})
	type result struct {
		code int
		body string
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		resp, err := unixHTTPClient(socket).Post("http://unix/api/v1/processes/sleeper/stdin", "application/json", strings.NewReader(string(body)))
		if err != nil {
			done <- result{0, err.Error()}
			return
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		done <- result{resp.StatusCode, string(b)}
	}()

	time.Sleep(500 * time.Millisecond)
	statusDone := make(chan error, 1)
	go func() { _, err := getProcessInfo(client, "sleeper"); statusDone <- err }()
	select {
	case err := <-statusDone:
		if err != nil {
			t.Errorf("status during the write: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("status hung behind the stdin write")
	}

	select {
	case r := <-done:
		if r.code != http.StatusConflict || !strings.Contains(r.body, "process sleeper: stdin is full (the process is not reading it)") {
			t.Errorf("write: %d %s", r.code, r.body)
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("write took %v", d)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("stdin write never gave up")
	}

	resp, err := unixHTTPClient(socket).Post("http://unix/api/v1/processes/sleeper/stdin", "application/json", strings.NewReader(`{"data":"`+strings.Repeat("y", 1<<20+1)+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(string(b), "stdin data exceeds 1048576 bytes") {
		t.Errorf("oversized: %d %s", resp.StatusCode, b)
	}
}
