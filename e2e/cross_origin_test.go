//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/testutil"
)

// Cross-origin protection of the REST API (SEC-025): a browser request from
// another site cannot change state, on the TCP listener or the Unix socket;
// same-origin, non-browser and safe requests go through.

// originRequest sends method url with the given headers (and Basic auth
// admin:pass unless pass is empty) through client, and returns the status,
// Content-Type and body.
func originRequest(t *testing.T, client *http.Client, method, url, pass string, headers map[string]string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pass != "" {
		req.SetBasicAuth("admin", pass)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
}

// unixHTTPClient talks HTTP over the daemon's Unix socket.
func unixHTTPClient(socket string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}},
	}
}

func TestCrossOrigin_StateChangesRejected(t *testing.T) {
	t.Parallel()
	d := startWebUIDaemon(t, false)
	cross := map[string]string{"Sec-Fetch-Site": "cross-site"}
	wantForbidden := func(what string, code int, ctype, body string) {
		t.Helper()
		var e struct{ Error, Code string }
		if code != http.StatusForbidden || ctype != "application/json" || json.Unmarshal([]byte(body), &e) != nil ||
			e.Error != "cross-origin request rejected" || e.Code != "FORBIDDEN" {
			t.Errorf("%s: %d %q %s; want 403 application/json with error \"cross-origin request rejected\" and code FORBIDDEN", what, code, ctype, body)
		}
	}

	// A cross-site stop is refused on both listeners.
	code, ctype, body := originRequest(t, webClient, "POST", d.base+"/api/v1/processes/sleeper/stop", "secret", cross)
	wantForbidden("TCP cross-site stop", code, ctype, body)
	code, ctype, body = originRequest(t, unixHTTPClient(d.socket), "POST", "http://unix/api/v1/processes/sleeper/stop", "", cross)
	wantForbidden("Unix socket cross-site stop", code, ctype, body)
	// Without Sec-Fetch-Site, an Origin that does not match the Host is
	// refused too.
	code, ctype, body = originRequest(t, webClient, "POST", d.base+"/api/v1/processes/sleeper/stop", "secret", map[string]string{"Origin": "http://evil.example"})
	wantForbidden("Origin mismatch", code, ctype, body)
	// Shutdown and group routes are covered as well.
	for _, path := range []string{"/api/v1/shutdown", "/api/v1/groups/sleeper/stop"} {
		code, ctype, body = originRequest(t, webClient, "POST", d.base+path, "secret", cross)
		wantForbidden("cross-site POST "+path, code, ctype, body)
	}
	// The check runs before authentication: wrong credentials still get 403.
	code, ctype, body = originRequest(t, webClient, "POST", d.base+"/api/v1/processes/sleeper/stop", "wrong", cross)
	wantForbidden("cross-site with wrong credentials", code, ctype, body)

	// Nothing was stopped and the daemon is still up.
	if code, _, _ := originRequest(t, webClient, "GET", d.base+"/healthz", "", nil); code != http.StatusOK {
		t.Fatalf("healthz %d after the refused shutdown, want 200", code)
	}
	if info, _ := getProcessInfo(d.client, "sleeper"); info.State != "RUNNING" {
		t.Fatalf("sleeper %s after refused requests, want RUNNING", info.State)
	}
}

// The Web UI escapes hostile process names in text and attributes,
// percent-encodes them in links, sends its CSP and nosniff on pages, assets
// and error pages, and its same-origin actions go through while cross-site
// ones do not (SEC-026).
func TestWebUI_SecurityEncodingAndHeaders(t *testing.T) {
	t.Parallel()
	addr := fmt.Sprintf("127.0.0.1:%d", testutil.FreeTCPPort(t))
	client, _ := startDaemon(t, fmt.Sprintf(`[server.http]
enabled = true
listen = %q
username = "admin"
%s = %q

[server.web]
enabled = true

[programs.web]
command = "/bin/sleep 300"
startsecs = 0

[programs."a'b\"<c>&d"]
command = "/bin/sleep 300"
startsecs = 0

[programs."x y#?"]
command = "/bin/sleep 300"
startsecs = 0

[programs.idle]
command = "/bin/sleep 300"
autostart = false
startsecs = 0
`, addr, "pass"+"word", hashPassword(t, "secret")))
	waitForTCP(t, addr, 10*time.Second)
	waitForState(t, client, "web", "RUNNING", 10*time.Second)
	base := "http://" + addr

	resp, page := webRequest(t, webClient, "GET", base+"/ui/", true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ui/: %d", resp.StatusCode)
	}
	if !strings.Contains(page, `data-name="a&#39;b&#34;&lt;c&gt;&amp;d"`) || !strings.Contains(page, ">a&#39;b&#34;&lt;c&gt;&amp;d<") {
		t.Errorf("status page does not escape the hostile name in its cell and data-name")
	}
	if strings.Contains(page, `a'b"<c>&d`) || strings.Contains(page, "<c>") {
		t.Errorf("status page carries the hostile name unescaped")
	}
	if !strings.Contains(page, `href="/ui/log/x%20y%23%3F/stdout"`) {
		t.Errorf("status page does not percent-encode the log link of %q", "x y#?")
	}

	const csp = "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	for path, status := range map[string]int{
		"/ui/":                  http.StatusOK,
		"/ui/log/web/stdout":    http.StatusOK,
		"/ui/log/web/stdin":     http.StatusBadRequest,
		"/ui/log/nosuch/stdout": http.StatusNotFound,
		"/ui/static/app.js":     http.StatusOK,
		"/ui/static/nope.js":    http.StatusNotFound,
	} {
		resp, _ := webRequest(t, webClient, "GET", base+path, true)
		if resp.StatusCode != status || resp.Header.Get("Content-Security-Policy") != csp || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("GET %s: %d, CSP %q, nosniff %q; want %d with the policy and nosniff", path, resp.StatusCode,
				resp.Header.Get("Content-Security-Policy"), resp.Header.Get("X-Content-Type-Options"), status)
		}
	}

	// The page's own action, a same-origin POST, starts a process; the same
	// request from another site does not.
	code, _, body := originRequest(t, webClient, "POST", base+"/api/v1/processes/idle/start", "secret", map[string]string{"Sec-Fetch-Site": "cross-site"})
	if code != http.StatusForbidden {
		t.Errorf("cross-site start: %d %s, want 403", code, body)
	}
	if info, _ := getProcessInfo(client, "idle"); info.State != "STOPPED" {
		t.Fatalf("idle %s after a cross-site start, want STOPPED", info.State)
	}
	code, _, body = originRequest(t, webClient, "POST", base+"/api/v1/processes/idle/start", "secret", map[string]string{"Sec-Fetch-Site": "same-origin"})
	if code != http.StatusOK {
		t.Errorf("same-origin start: %d %s, want 200", code, body)
	}
	waitForState(t, client, "idle", "RUNNING", 10*time.Second)
}

func TestCrossOrigin_AllowedRequests(t *testing.T) {
	t.Parallel()
	d := startWebUIDaemon(t, false)

	// Safe methods are allowed cross-site, the event stream too.
	code, _, body := originRequest(t, webClient, "GET", d.base+"/api/v1/processes", "secret", map[string]string{"Sec-Fetch-Site": "cross-site"})
	if code != http.StatusOK || !strings.Contains(body, `"name":"sleeper"`) {
		t.Errorf("cross-site GET /api/v1/processes: %d %s, want 200", code, body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", d.base+"/api/v1/events/stream", nil)
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := webClient.Do(req)
	if err != nil {
		t.Fatalf("cross-site event stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Errorf("cross-site event stream: %d %q, want 200 text/event-stream", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	_ = resp.Body.Close()

	// Same-origin and user-initiated (none) state changes are allowed.
	for _, site := range []string{"same-origin", "none"} {
		code, _, body := originRequest(t, webClient, "POST", d.base+"/api/v1/processes/sleeper/stop", "secret", map[string]string{"Sec-Fetch-Site": site})
		if code != http.StatusOK {
			t.Fatalf("Sec-Fetch-Site %s stop: %d %s, want 200", site, code, body)
		}
		waitForState(t, d.client, "sleeper", "STOPPED", 10*time.Second)
		if err := d.client.Start("sleeper"); err != nil {
			t.Fatal(err)
		}
		waitForState(t, d.client, "sleeper", "RUNNING", 10*time.Second)
	}
}
