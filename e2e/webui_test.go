//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/ctl"
	"github.com/kahiteam/kahi/internal/testutil"
)

// webUIDaemon is a running daemon with the TCP listener and, optionally, the
// Web UI enabled, plus one long-running program named sleeper (TEST-006).
type webUIDaemon struct {
	base   string // http://127.0.0.1:<port>
	socket string
	client *ctl.Client
}

var webClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func startWebUIDaemon(t *testing.T, webEnabled bool) webUIDaemon {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatalf("sleep not found: %v", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreeTCPPort(t))
	webSection := ""
	if webEnabled {
		webSection = "[server.web]\nenabled = true\n\n"
	}
	cfg := fmt.Sprintf(`[server.http]
enabled = true
listen = %q
username = "admin"
%s = %q

%s[programs.sleeper]
command = "%s 300"
autostart = true
startsecs = 0
stopwaitsecs = 2
`, addr, "pass"+"word", hashPassword(t, "secret"), webSection, sleep)

	client, socket := startDaemon(t, cfg)
	waitForTCP(t, addr, 10*time.Second)
	waitForState(t, client, "sleeper", "RUNNING", 10*time.Second)
	return webUIDaemon{base: "http://" + addr, socket: socket, client: client}
}

func waitForTCP(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("TCP listener %s not accepting after %v", addr, timeout)
}

func webRequest(t *testing.T, client *http.Client, method, url string, auth bool) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth {
		req.SetBasicAuth("admin", "secret")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestWebUIE2EServed(t *testing.T) {
	d := startWebUIDaemon(t, true)
	resp, body := webRequest(t, webClient, http.MethodGet, d.base+"/ui/", true)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /ui/: status %d, Content-Type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, `data-name="sleeper"`) {
		t.Error("the status page does not list sleeper")
	}
}

func TestWebUIE2EAuth(t *testing.T) {
	d := startWebUIDaemon(t, true)
	if resp, _ := webRequest(t, webClient, http.MethodGet, d.base+"/ui/", false); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /ui/ without credentials: status %d, want 401", resp.StatusCode)
	}
}

func TestWebUIE2EUnixSocket(t *testing.T) {
	d := startWebUIDaemon(t, true)
	unixClient := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", d.socket)
		},
	}}
	resp, body := webRequest(t, unixClient, http.MethodGet, "http://unix/ui/", false)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `data-name="sleeper"`) {
		t.Errorf("GET /ui/ over the socket: status %d, want 200 listing sleeper", resp.StatusCode)
	}
}

func TestWebUIE2EAssets(t *testing.T) {
	d := startWebUIDaemon(t, true)
	for path, ct := range map[string]string{
		"/ui/static/app.js":    "text/javascript; charset=utf-8",
		"/ui/static/style.css": "text/css; charset=utf-8",
	} {
		resp, body := webRequest(t, webClient, http.MethodGet, d.base+path, true)
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != ct ||
			resp.Header.Get("ETag") == "" || body == "" {
			t.Errorf("GET %s: status %d, Content-Type %q, ETag %q, %d bytes",
				path, resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("ETag"), len(body))
		}
	}
}

func TestWebUIE2ELogViewer(t *testing.T) {
	d := startWebUIDaemon(t, true)
	resp, body := webRequest(t, webClient, http.MethodGet, d.base+"/ui/log/sleeper/stdout", true)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `data-name="sleeper"`) {
		t.Errorf("GET /ui/log/sleeper/stdout: status %d", resp.StatusCode)
	}
}

func TestWebUIE2EBulk(t *testing.T) {
	d := startWebUIDaemon(t, true)
	if resp, body := webRequest(t, webClient, http.MethodPost, d.base+"/api/v1/all/stop", true); resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/all/stop: status %d, body %s", resp.StatusCode, body)
	}
	waitForState(t, d.client, "sleeper", "STOPPED", 10*time.Second)
	if resp, body := webRequest(t, webClient, http.MethodPost, d.base+"/api/v1/all/start", true); resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/v1/all/start: status %d, body %s", resp.StatusCode, body)
	}
	waitForState(t, d.client, "sleeper", "RUNNING", 10*time.Second)
}

func TestWebUIE2EDisabled(t *testing.T) {
	d := startWebUIDaemon(t, false)
	if resp, _ := webRequest(t, webClient, http.MethodGet, d.base+"/ui/", true); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /ui/ with [server.web] absent: status %d, want 404", resp.StatusCode)
	}
}

// runDaemonExpectFailure runs the daemon on a config it writes itself
// (startDaemon is not used: it waits for readiness) and returns stderr.
func runDaemonExpectFailure(t *testing.T, configTOML string) (string, error) {
	t.Helper()
	dir, err := os.MkdirTemp("", "k-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	full := fmt.Sprintf("[supervisor]\nshutdown_timeout = 5\n\n[server.unix]\nfile = %q\n\n%s",
		filepath.Join(dir, "kahi.sock"), configTOML)
	cfgPath := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(cfgPath, []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, kahiBinary, "daemon", "-c", cfgPath)
	cmd.Dir = dir
	cmd.Stderr = &stderr
	err = cmd.Run()
	return stderr.String(), err
}

func TestWebUIE2EInvalidConfig(t *testing.T) {
	stderr, err := runDaemonExpectFailure(t, "[server.web]\nenabled = true\n")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("daemon err = %v, want a non-zero exit; stderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "server.web.enabled requires server.http.enabled") {
		t.Errorf("stderr does not explain the rejection:\n%s", stderr)
	}
}
