//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// REST API (FUNC-026) and readiness (FUNC-030), as raw HTTP over the Unix
// socket so status codes, headers and bodies are checked exactly.

// apiResponse is one HTTP response from the daemon.
type apiResponse struct {
	status int
	header http.Header
	body   []byte
}

// apiDo sends method path over the Unix socket.
func apiDo(t *testing.T, socketPath, method, path, body string) apiResponse {
	t.Helper()
	hc := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://unix"+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return apiResponse{status: resp.StatusCode, header: resp.Header, body: b}
}

// jsonBody checks status and Content-Type and decodes the body into v.
func (r apiResponse) jsonBody(t *testing.T, what string, status int, v any) {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: status %d, want %d; body %s", what, r.status, status, r.body)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: Content-Type %q, want application/json", what, ct)
	}
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("%s: body %s: %v", what, r.body, err)
	}
}

// wantError checks a JSON error response.
func (r apiResponse) wantError(t *testing.T, what string, status int, msg, code string) {
	t.Helper()
	var e map[string]string
	r.jsonBody(t, what, status, &e)
	if e["error"] != msg || e["code"] != code {
		t.Errorf("%s: %v, want error %q code %q", what, e, msg, code)
	}
}

func TestREST_ProcessEndpoints(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "hup")
	ready := filepath.Join(dir, "ready")
	script := writeScript(t, dir, "web.sh",
		"trap 'echo HUP > "+marker+"' HUP\necho ready > "+ready+"\necho web-started\nwhile true; do sleep 0.1; done")
	client, socket := startDaemon(t, `
[programs.web]
command = "`+script+`"
autostart = true
startsecs = 0
stopwaitsecs = 2
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	waitForMarker(t, ready, "ready", 5*time.Second)

	// List: a JSON array of process info objects.
	var list []processInfo
	apiDo(t, socket, "GET", "/api/v1/processes", "").jsonBody(t, "list", http.StatusOK, &list)
	if len(list) != 1 || list[0].Name != "web" || list[0].State != "RUNNING" || list[0].PID == 0 {
		t.Fatalf("list = %+v", list)
	}

	// Signal: HUP reaches the child.
	var sig map[string]string
	apiDo(t, socket, "POST", "/api/v1/processes/web/signal", `{"signal":"HUP"}`).jsonBody(t, "signal", http.StatusOK, &sig)
	if sig["status"] != "signaled" || sig["name"] != "web" || sig["signal"] != "HUP" {
		t.Errorf("signal body %v", sig)
	}
	waitForMarker(t, marker, "HUP", 5*time.Second)

	// Invalid signal and unknown process: JSON errors.
	apiDo(t, socket, "POST", "/api/v1/processes/web/signal", `{"signal":"FOO"}`).
		wantError(t, "invalid signal", http.StatusBadRequest, `process web: invalid signal "FOO"`, "BAD_REQUEST")
	apiDo(t, socket, "GET", "/api/v1/processes/nope", "").
		wantError(t, "unknown process", http.StatusNotFound, "no such process: nope", "NOT_FOUND")
	apiDo(t, socket, "POST", "/api/v1/processes/nope/start", "").
		wantError(t, "start unknown", http.StatusNotFound, "no such process: nope", "NOT_FOUND")

	// Unsupported method and unknown route: JSON too, with Allow on 405.
	r := apiDo(t, socket, "GET", "/api/v1/shutdown", "")
	r.wantError(t, "GET shutdown", http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	if r.header.Get("Allow") == "" {
		t.Error("405 without an Allow header")
	}
	apiDo(t, socket, "DELETE", "/api/v1/processes/web", "").
		wantError(t, "DELETE process", http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
	apiDo(t, socket, "GET", "/api/v1/nosuchroute", "").
		wantError(t, "unknown route", http.StatusNotFound, "not found", "NOT_FOUND")

	// Log: the captured output, as text.
	r = apiDo(t, socket, "GET", "/api/v1/processes/web/log/stdout", "")
	if r.status != http.StatusOK || !strings.Contains(string(r.body), "web-started") ||
		!strings.HasPrefix(r.header.Get("Content-Type"), "text/plain") {
		t.Errorf("log: %d %q %q", r.status, r.header.Get("Content-Type"), r.body)
	}

	// Stop, start and restart.
	var act map[string]string
	apiDo(t, socket, "POST", "/api/v1/processes/web/stop", "").jsonBody(t, "stop", http.StatusOK, &act)
	if act["status"] != "stopped" || act["name"] != "web" {
		t.Errorf("stop body %v", act)
	}
	waitForState(t, client, "web", "STOPPED", 10*time.Second)
	apiDo(t, socket, "POST", "/api/v1/processes/web/start", "").jsonBody(t, "start", http.StatusOK, &act)
	if act["status"] != "started" || act["name"] != "web" {
		t.Errorf("start body %v", act)
	}
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	before, _ := getProcessInfo(client, "web")
	apiDo(t, socket, "POST", "/api/v1/processes/web/restart", "").jsonBody(t, "restart", http.StatusOK, &act)
	if act["status"] != "restarted" || act["name"] != "web" {
		t.Errorf("restart body %v", act)
	}
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	if after, _ := getProcessInfo(client, "web"); after.PID == before.PID {
		t.Errorf("restart kept pid %d", after.PID)
	}

	// Config: the loaded programs.
	var cfg struct {
		Programs map[string]struct{ Command string }
	}
	apiDo(t, socket, "GET", "/api/v1/config", "").jsonBody(t, "config", http.StatusOK, &cfg)
	if cfg.Programs["web"].Command != script {
		t.Errorf("config programs = %+v", cfg.Programs)
	}
}

// POST /api/v1/config/reload re-reads the file and returns the diff.
func TestREST_Reload(t *testing.T) {
	client, socket := startDaemon(t, `
[programs.old]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	waitForState(t, client, "old", "RUNNING", 5*time.Second)
	rewriteConfig(t, socket, `
[programs.added]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	var diff struct {
		Status                  string
		Added, Changed, Removed []string
	}
	apiDo(t, socket, "POST", "/api/v1/config/reload", "").jsonBody(t, "reload", http.StatusOK, &diff)
	if diff.Status != "reloaded" || strings.Join(diff.Added, ",") != "added" || strings.Join(diff.Removed, ",") != "old" || len(diff.Changed) != 0 {
		t.Fatalf("reload body %+v", diff)
	}
	waitForState(t, client, "added", "RUNNING", 5*time.Second)
}

// POST /api/v1/shutdown starts a graceful shutdown.
func TestREST_Shutdown(t *testing.T) {
	_, _, done, _, socket := shutdownDaemon(t, 10, `
[programs.worker]
command = "/bin/sleep 300"
autostart = true
startsecs = 0
`)
	var body map[string]string
	apiDo(t, socket, "POST", "/api/v1/shutdown", "").jsonBody(t, "shutdown", http.StatusOK, &body)
	if body["status"] != "shutting_down" {
		t.Errorf("shutdown body %v", body)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("daemon did not exit after POST /api/v1/shutdown")
	}
}

// readyz is one /readyz response.
type readyz struct {
	Status           string
	Pending, Failing []string
}

func getReadyz(t *testing.T, socket, query string, status int) readyz {
	t.Helper()
	var r readyz
	apiDo(t, socket, "GET", "/readyz"+query, "").jsonBody(t, "readyz"+query, status, &r)
	return r
}

func TestReadyz_States(t *testing.T) {
	dir := t.TempDir()
	client, socket := startDaemon(t, `
[programs.web]
command = "/bin/sleep 300"
autostart = true
startsecs = 0

[programs.api]
command = "/bin/sleep 300"
autostart = true
startsecs = 0

[programs.slow]
command = "/bin/sleep 300"
autostart = true
startsecs = 3

[programs.manual]
command = "/bin/sleep 300"
autostart = false

[programs.broken]
command = "`+filepath.Join(dir, "missing")+`"
autostart = false
startretries = 0
`)
	// slow is still STARTING: pending, not failing.
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	if r := getReadyz(t, socket, "", http.StatusServiceUnavailable); r.Status != "not_ready" ||
		strings.Join(r.Pending, ",") != "slow" || len(r.Failing) != 0 {
		t.Fatalf("while slow is STARTING: %+v", r)
	}

	// Every autostart process RUNNING: ready, although manual (autostart =
	// false) is STOPPED.
	waitForState(t, client, "slow", "RUNNING", 10*time.Second)
	if r := getReadyz(t, socket, "", http.StatusOK); r.Status != "ready" {
		t.Fatalf("all running: %+v", r)
	}
	if r := getReadyz(t, socket, "?process=web,api", http.StatusOK); r.Status != "ready" {
		t.Fatalf("web,api running: %+v", r)
	}

	// A stopped process named in the filter is failing.
	if err := client.Stop("api"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, client, "api", "STOPPED", 10*time.Second)
	if r := getReadyz(t, socket, "?process=web,api", http.StatusServiceUnavailable); r.Status != "not_ready" ||
		len(r.Pending) != 0 || strings.Join(r.Failing, ",") != "api" {
		t.Fatalf("api stopped: %+v", r)
	}

	// FATAL is not ready: it is listed as failing.
	if err := client.Start("broken"); err == nil {
		t.Fatal("start of a missing command succeeded")
	}
	waitForState(t, client, "broken", "FATAL", 5*time.Second)
	if r := getReadyz(t, socket, "?process=broken", http.StatusServiceUnavailable); strings.Join(r.Failing, ",") != "broken" {
		t.Fatalf("broken FATAL: %+v", r)
	}

	// An unknown name is a 400.
	apiDo(t, socket, "GET", "/readyz?process=unknown", "").
		wantError(t, "readyz unknown", http.StatusBadRequest, "no such process: unknown", "BAD_REQUEST")
}

// An autostart program that went FATAL keeps the daemon not ready.
func TestReadyz_FatalAutostartNotReady(t *testing.T) {
	dir := t.TempDir()
	client, socket := startDaemon(t, `
[programs.web]
command = "/bin/sleep 300"
autostart = true
startsecs = 0

[programs.broken]
command = "`+filepath.Join(dir, "missing")+`"
autostart = true
startretries = 0
`)
	waitForState(t, client, "web", "RUNNING", 5*time.Second)
	waitForState(t, client, "broken", "FATAL", 5*time.Second)
	if r := getReadyz(t, socket, "", http.StatusServiceUnavailable); len(r.Pending) != 0 || strings.Join(r.Failing, ",") != "broken" {
		t.Fatalf("readyz with broken FATAL: %+v", r)
	}
}

// /readyz needs no credentials on the TCP listener.
func TestReadyz_NoAuthOnTCP(t *testing.T) {
	d := startWebUIDaemon(t, false)
	resp, body := webRequest(t, webClient, http.MethodGet, d.base+"/readyz", false)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"status":"ready"`) {
		t.Fatalf("GET /readyz over TCP without credentials: %d %s", resp.StatusCode, body)
	}
}
