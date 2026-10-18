package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/events"
)

// SEC-025 / ADR-028: the REST API authenticates with HTTP Basic credentials,
// which a browser caches per origin and attaches to cross-site requests. With
// no Origin or Sec-Fetch-Site check, a page on another site could make the
// browser start, stop, signal or shut down supervised processes. The protected
// handler rejects unsafe-method requests that a browser marks as cross-origin.

// Counting wrappers record calls the shared mocks do not, so a test can prove a
// rejected request never reached the manager.
type countingProcesses struct {
	*mockProcessManager
	stops atomic.Int32
}

func (c *countingProcesses) Stop(name string) error {
	c.stops.Add(1)
	return c.mockProcessManager.Stop(name)
}

type countingGroups struct {
	*mockGroupManager
	stops atomic.Int32
}

func (c *countingGroups) StopGroup(name string) error {
	c.stops.Add(1)
	return c.mockGroupManager.StopGroup(name)
}

type countingDaemon struct {
	*mockDaemonInfo
	shutdowns atomic.Int32
}

func (c *countingDaemon) Shutdown() { c.shutdowns.Add(1) }

func crossOriginServer() (*Server, *countingProcesses, *countingGroups, *countingDaemon) {
	pm := &countingProcesses{mockProcessManager: &mockProcessManager{
		processes: []ProcessInfo{{Name: "web", Group: "web", State: "RUNNING", PID: 1234}},
	}}
	gm := &countingGroups{mockGroupManager: &mockGroupManager{groups: []string{"web"}}}
	cm := &mockConfigManager{cfg: map[string]string{}}
	di := &countingDaemon{mockDaemonInfo: &mockDaemonInfo{ready: true}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv := NewServer(Config{}, pm, gm, cm, di, events.NewBus(logger), logger)
	srv.authUser = "admin"
	srv.authPass = testPassHash
	return srv, pm, gm, di
}

// post builds an authenticated POST. httptest.NewRequest sets the connection
// address to 192.0.2.1, so this is a TCP request and authentication applies.
func post(path string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.SetBasicAuth("admin", "secret")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func assertForbidden(t *testing.T, status int, header http.Header, body []byte) {
	t.Helper()
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", status, body)
	}
	if ct := header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("deny body is not JSON: %v: %s", err, body)
	}
	if got["code"] != "FORBIDDEN" || got["error"] != "cross-origin request rejected" {
		t.Fatalf("deny body = %v, want code FORBIDDEN, error \"cross-origin request rejected\"", got)
	}
}

var crossSite = map[string]string{"Sec-Fetch-Site": "cross-site"}

func TestCrossOriginBlocksStateChange(t *testing.T) {
	srv, pm, _, _ := crossOriginServer()

	w := serve(srv.handler, post("/api/v1/processes/web/stop", crossSite))
	assertForbidden(t, w.Code, w.Header(), w.Body.Bytes())
	if n := pm.stops.Load(); n != 0 {
		t.Fatalf("rejected request still reached Stop %d time(s)", n)
	}

	// The same request through the bare route table succeeds: the route itself
	// is unprotected, and the wrapper is what blocks it.
	w = serve(srv.mux, post("/api/v1/processes/web/stop", crossSite))
	if w.Code != http.StatusOK {
		t.Fatalf("bare mux status = %d, want 200", w.Code)
	}
	if n := pm.stops.Load(); n != 1 {
		t.Fatalf("bare mux Stop calls = %d, want 1", n)
	}
}

// Browsers without Sec-Fetch-Site fall back to comparing Origin with Host. The
// Host must be set on the request itself: httptest ignores a Host header.
func TestCrossOriginOriginMismatch(t *testing.T) {
	srv, pm, _, _ := crossOriginServer()
	req := post("/api/v1/processes/web/stop", map[string]string{"Origin": "http://evil.example"})
	req.Host = "kahi.local"

	w := serve(srv.handler, req)
	assertForbidden(t, w.Code, w.Header(), w.Body.Bytes())
	if n := pm.stops.Load(); n != 0 {
		t.Fatalf("rejected request still reached Stop %d time(s)", n)
	}
}

func TestCrossOriginSameOriginAllowed(t *testing.T) {
	srv, pm, _, _ := crossOriginServer()
	for _, site := range []string{"same-origin", "none"} {
		w := serve(srv.handler, post("/api/v1/processes/web/stop", map[string]string{"Sec-Fetch-Site": site}))
		if w.Code != http.StatusOK {
			t.Fatalf("Sec-Fetch-Site %q: status = %d, want 200", site, w.Code)
		}
	}
	if n := pm.stops.Load(); n != 2 {
		t.Fatalf("Stop calls = %d, want 2", n)
	}
}

// kahi ctl, curl and scripts send neither Origin nor Sec-Fetch-Site.
func TestCrossOriginNonBrowserAllowed(t *testing.T) {
	srv, pm, _, _ := crossOriginServer()
	w := serve(srv.handler, post("/api/v1/processes/web/stop", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if n := pm.stops.Load(); n != 1 {
		t.Fatalf("Stop calls = %d, want 1", n)
	}
}

func TestCrossOriginSafeMethodsAllowed(t *testing.T) {
	srv, _, _, _ := crossOriginServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/processes", nil)
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if w := serve(srv.handler, req); w.Code != http.StatusOK {
		t.Fatalf("cross-site GET status = %d, want 200", w.Code)
	}

	// An SSE stream runs until its request context ends, so bound it.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/events/stream", nil).WithContext(ctx)
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := serve(srv.handler, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cross-site SSE status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("cross-site SSE Content-Type = %q, want text/event-stream", ct)
	}
}

// Every unsafe route is covered, not only process stop. Shutdown is the most
// severe: it stops the daemon and every child.
func TestCrossOriginCoversAllUnsafeRoutes(t *testing.T) {
	srv, _, gm, di := crossOriginServer()

	for _, path := range []string{
		"/api/v1/shutdown",
		"/api/v1/groups/web/stop",
		"/api/v1/config/reload",
		"/api/v1/processes/web/signal",
		"/api/v1/processes/web/stdin",
	} {
		w := serve(srv.handler, post(path, crossSite))
		assertForbidden(t, w.Code, w.Header(), w.Body.Bytes())
	}

	// The shutdown handler replies first and shuts down 100ms later, so wait
	// past that before asserting it never ran.
	time.Sleep(250 * time.Millisecond)
	if n := di.shutdowns.Load(); n != 0 {
		t.Fatalf("rejected request still reached Shutdown %d time(s)", n)
	}
	if n := gm.stops.Load(); n != 0 {
		t.Fatalf("rejected request still reached StopGroup %d time(s)", n)
	}
}

// The check runs before authentication, so a cross-site request is refused the
// same way whatever its credentials, and learns nothing about them.
func TestCrossOriginBeforeAuth(t *testing.T) {
	srv, _, _, _ := crossOriginServer()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/processes/web/stop", nil)
	req.SetBasicAuth("admin", "wrong")
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	w := serve(srv.handler, req)
	assertForbidden(t, w.Code, w.Header(), w.Body.Bytes())
}

// Both servers must serve the protected handler. This is checked behaviourally:
// the protected handler is an http.HandlerFunc, and comparing func-typed
// handlers with == panics. The Unix leg matters even though browsers cannot
// reach the socket directly: behind a reverse proxy the socket path skips
// authentication, so this protection is the only thing that stops a cross-site
// request there.
func TestCrossOriginBothServersWired(t *testing.T) {
	srv, pm, _, _ := crossOriginServer()

	if err := srv.StartTCP("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join("/tmp", fmt.Sprintf("kahi-xorigin-%d.sock", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(sockPath) })
	if err := srv.StartUnix(sockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Stop(context.Background()) }()

	for name, h := range map[string]http.Handler{"tcp": srv.tcpServer.Handler, "unix": srv.unixServer.Handler} {
		if _, bare := h.(*http.ServeMux); bare {
			t.Fatalf("%s server serves the bare route mux, not the protected handler", name)
		}
	}

	unixClient := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
		},
	}}
	for name, tc := range map[string]struct {
		client *http.Client
		url    string
	}{
		"tcp":  {http.DefaultClient, "http://" + srv.TCPAddr() + "/api/v1/processes/web/stop"},
		"unix": {unixClient, "http://unix/api/v1/processes/web/stop"},
	} {
		req, err := http.NewRequest(http.MethodPost, tc.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("admin", "secret")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		resp, err := tc.client.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s server: status = %d, want 403; body %s", name, resp.StatusCode, body)
		}
		assertForbidden(t, resp.StatusCode, resp.Header, body)
	}
	if n := pm.stops.Load(); n != 0 {
		t.Fatalf("rejected requests still reached Stop %d time(s)", n)
	}
}
