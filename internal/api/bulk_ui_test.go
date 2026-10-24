package api

import (
	"context"
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

// bulkProcesses overrides the bulk methods of mockProcessManager with
// injectable functions and call counters.
type bulkProcesses struct {
	*mockProcessManager
	start, stop, restart func() []ActionResult
	shuttingDown         atomic.Bool
	startCalls           atomic.Int32
	stopCalls            atomic.Int32
	restartCalls         atomic.Int32
	singleStarts         atomic.Int32
}

func (b *bulkProcesses) StartAllOrdered() []ActionResult {
	b.startCalls.Add(1)
	if b.start != nil {
		return b.start()
	}
	return []ActionResult{}
}

func (b *bulkProcesses) StopAllOrdered() []ActionResult {
	b.stopCalls.Add(1)
	if b.stop != nil {
		return b.stop()
	}
	return []ActionResult{}
}

func (b *bulkProcesses) RestartAllOrdered() []ActionResult {
	b.restartCalls.Add(1)
	if b.restart != nil {
		return b.restart()
	}
	return []ActionResult{}
}

func (b *bulkProcesses) ShuttingDown() bool { return b.shuttingDown.Load() }

func (b *bulkProcesses) Start(name string) error {
	b.singleStarts.Add(1)
	return b.mockProcessManager.Start(name)
}

func newBulkProcesses() *bulkProcesses {
	return &bulkProcesses{mockProcessManager: &mockProcessManager{processes: []ProcessInfo{
		{Name: "web", Group: "web", State: "STOPPED"},
		{Name: "all", Group: "all", State: "STOPPED"},
	}}}
}

// newTestServer builds a Server with credentials over the given process
// manager, daemon and optional UI handler.
func newTestServer(pm ProcessManager, di DaemonInfo, ui http.Handler) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := Config{Username: "admin", Password: testPassHash, UI: ui}
	return NewServer(cfg, pm, &mockGroupManager{}, &mockConfigManager{}, di,
		events.NewBus(logger), logger)
}

func startTestTCP(t *testing.T, srv *Server) string {
	t.Helper()
	if err := srv.StartTCP("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	return "http://" + srv.TCPAddr()
}

// noRedirectClient returns responses as sent, without following redirects.
var noRedirectClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func doRequest(t *testing.T, method, url string, auth bool, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth {
		req.SetBasicAuth("admin", "secret")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// waitClosed fails the test instead of hanging when ch is not closed in time,
// for example when the route under test does not exist yet.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// --- Bulk action routes (FUNC-083) ---

func TestBulkAPIRoutes(t *testing.T) {
	pm := newBulkProcesses()
	pm.start = func() []ActionResult {
		return []ActionResult{{Name: "a", Status: ActionOK}, {Name: "b", Status: ActionError, Error: "boom"}}
	}
	pm.stop = func() []ActionResult { return []ActionResult{{Name: "b", Status: ActionSkipped}} }
	srv := newTestServer(pm, &mockDaemonInfo{}, nil)

	for _, tc := range []struct{ path, want string }{
		{"/api/v1/all/start", `{"results":[{"name":"a","status":"ok"},{"name":"b","status":"error","error":"boom"}]}`},
		{"/api/v1/all/stop", `{"results":[{"name":"b","status":"skipped"}]}`},
		{"/api/v1/all/restart", `{"results":[]}`},
	} {
		w := serve(srv.handler, post(tc.path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d, body %s", tc.path, w.Code, w.Body)
		}
		if got := strings.TrimSuffix(w.Body.String(), "\n"); got != tc.want {
			t.Errorf("%s: body %s, want %s", tc.path, got, tc.want)
		}
	}
	if pm.startCalls.Load() != 1 || pm.stopCalls.Load() != 1 || pm.restartCalls.Load() != 1 {
		t.Errorf("calls start=%d stop=%d restart=%d, want 1 each",
			pm.startCalls.Load(), pm.stopCalls.Load(), pm.restartCalls.Load())
	}

	if w := serve(srv.handler, post("/api/v1/all/foo", nil)); w.Code != http.StatusNotFound {
		t.Errorf("POST /api/v1/all/foo: status %d, want 404", w.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/all/stop", nil)
	if w := serve(srv.handler, req); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/v1/all/stop: status %d, want 405", w.Code)
	}
}

func TestBulkAPIConflict(t *testing.T) {
	pm := newBulkProcesses()
	entered, release := make(chan struct{}), make(chan struct{})
	pm.stop = func() []ActionResult { close(entered); <-release; return []ActionResult{} }
	srv := newTestServer(pm, &mockDaemonInfo{}, nil)

	first := make(chan int, 1)
	go func() { first <- serve(srv.handler, post("/api/v1/all/stop", nil)).Code }()
	waitClosed(t, entered, "the first bulk call to reach StopAllOrdered")

	w := serve(srv.handler, post("/api/v1/all/start", nil))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"CONFLICT"`) ||
		!strings.Contains(w.Body.String(), "bulk action in progress") {
		t.Errorf("concurrent bulk call: status %d, body %s; want 409 CONFLICT", w.Code, w.Body)
	}
	if pm.startCalls.Load() != 0 {
		t.Error("a refused bulk call still acted")
	}
	close(release)
	select {
	case code := <-first:
		if code != http.StatusOK {
			t.Errorf("first bulk call: status %d, want 200", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first bulk call did not finish")
	}
}

func TestBulkAPIClientDisconnect(t *testing.T) {
	pm := newBulkProcesses()
	entered, release := make(chan struct{}), make(chan struct{})
	var completed atomic.Bool
	pm.stop = func() []ActionResult {
		close(entered)
		<-release
		completed.Store(true)
		return []ActionResult{}
	}
	srv := newTestServer(pm, &mockDaemonInfo{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		serve(srv.handler, post("/api/v1/all/stop", nil).WithContext(ctx))
		close(done)
	}()
	waitClosed(t, entered, "the bulk call to reach StopAllOrdered")
	cancel()
	close(release)
	waitClosed(t, done, "the bulk call to return")

	if !completed.Load() {
		t.Error("the bulk action did not run to completion after the client went away")
	}
	pm.stop = nil
	if w := serve(srv.handler, post("/api/v1/all/stop", nil)); w.Code != http.StatusOK {
		t.Errorf("follow-up bulk call: status %d, want 200 (mutex released)", w.Code)
	}
}

func TestBulkAPIShutdown(t *testing.T) {
	for name, setup := range map[string]func(*bulkProcesses, *mockDaemonInfo){
		"daemon flag":  func(_ *bulkProcesses, di *mockDaemonInfo) { di.shuttingDown = true },
		"manager flag": func(pm *bulkProcesses, _ *mockDaemonInfo) { pm.shuttingDown.Store(true) },
	} {
		pm, di := newBulkProcesses(), &mockDaemonInfo{}
		setup(pm, di)
		srv := newTestServer(pm, di, nil)
		w := serve(srv.handler, post("/api/v1/all/start", nil))
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"SHUTTING_DOWN"`) ||
			!strings.Contains(w.Body.String(), "daemon is shutting down") {
			t.Errorf("%s: status %d, body %s; want 409 SHUTTING_DOWN", name, w.Code, w.Body)
		}
		if pm.startCalls.Load() != 0 {
			t.Errorf("%s: StartAllOrdered called during shutdown", name)
		}
	}
}

func TestBulkAPIShutdownBeforeConflict(t *testing.T) {
	pm, di := newBulkProcesses(), &mockDaemonInfo{}
	entered, release := make(chan struct{}), make(chan struct{})
	pm.stop = func() []ActionResult { close(entered); <-release; return []ActionResult{} }
	srv := newTestServer(pm, di, nil)

	go serve(srv.handler, post("/api/v1/all/stop", nil))
	waitClosed(t, entered, "the first bulk call to reach StopAllOrdered")
	defer close(release)
	pm.shuttingDown.Store(true)

	w := serve(srv.handler, post("/api/v1/all/start", nil))
	if !strings.Contains(w.Body.String(), `"code":"SHUTTING_DOWN"`) {
		t.Errorf("status %d, body %s; want SHUTTING_DOWN to win over CONFLICT", w.Code, w.Body)
	}
}

func TestBulkAPIAuthAndCSRF(t *testing.T) {
	pm := newBulkProcesses()
	srv := newTestServer(pm, &mockDaemonInfo{}, nil)
	base := startTestTCP(t, srv)

	if resp, _ := doRequest(t, http.MethodPost, base+"/api/v1/all/stop", false, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no credentials: status %d, want 401", resp.StatusCode)
	}
	w := serve(srv.handler, post("/api/v1/all/stop", map[string]string{"Sec-Fetch-Site": "cross-site"}))
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site: status %d, want 403", w.Code)
	}
	if n := pm.stopCalls.Load(); n != 0 {
		t.Errorf("StopAllOrdered called %d times by refused requests", n)
	}
}

func TestBulkAPIProcessNamedAll(t *testing.T) {
	pm := newBulkProcesses()
	srv := newTestServer(pm, &mockDaemonInfo{}, nil)
	if w := serve(srv.handler, post("/api/v1/processes/all/start", nil)); w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
	if pm.singleStarts.Load() != 1 || pm.startCalls.Load() != 0 {
		t.Errorf("Start=%d StartAllOrdered=%d, want 1 and 0", pm.singleStarts.Load(), pm.startCalls.Load())
	}
}

// --- UI mount (FUNC-084, ADR-029) ---

// stubUI answers every request with "ui:" and the path.
var stubUI = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = fmt.Fprint(w, "ui:"+r.URL.Path)
})

func TestUIMountDisabled(t *testing.T) {
	srv := newTestServer(newBulkProcesses(), &mockDaemonInfo{}, nil)
	for _, path := range []string{"/ui/", "/ui/static/app.js", "/"} {
		w := serve(srv.handler, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %s with the UI disabled: status %d, want 404", path, w.Code)
		}
	}
}

func TestUIMountAuth(t *testing.T) {
	srv := newTestServer(newBulkProcesses(), &mockDaemonInfo{}, stubUI)
	base := startTestTCP(t, srv)

	for _, path := range []string{"/ui/", "/ui/a/b", "/ui/static/zzz", "/"} {
		resp, _ := doRequest(t, http.MethodGet, base+path, false, nil)
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("GET %s without credentials: status %d, WWW-Authenticate %q; want 401 with a challenge",
				path, resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	}
	for _, path := range []string{"/ui/", "/ui/a/b", "/ui/static/zzz"} {
		resp, body := doRequest(t, http.MethodGet, base+path, true, nil)
		if resp.StatusCode != http.StatusOK || body != "ui:"+path {
			t.Errorf("GET %s with credentials: status %d, body %q; want the UI handler", path, resp.StatusCode, body)
		}
	}
	resp, _ := doRequest(t, http.MethodGet, base+"/", true, nil)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ui/" {
		t.Errorf("GET / with credentials: status %d, Location %q; want 302 to /ui/",
			resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := doRequest(t, http.MethodGet, base+"/api/v1/processes", true, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/v1/processes with the UI mounted: status %d, want 200", resp.StatusCode)
	}
}

func TestUIMountTrailingSlash(t *testing.T) {
	srv := newTestServer(newBulkProcesses(), &mockDaemonInfo{}, stubUI)
	base := startTestTCP(t, srv)
	resp, _ := doRequest(t, http.MethodGet, base+"/ui", false, nil)
	if resp.StatusCode != http.StatusTemporaryRedirect || resp.Header.Get("Location") != "/ui/" {
		t.Errorf("GET /ui: status %d, Location %q; want 307 to /ui/", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestUIMountUnixSocket(t *testing.T) {
	srv := newTestServer(newBulkProcesses(), &mockDaemonInfo{}, stubUI)
	sockPath := filepath.Join("/tmp", fmt.Sprintf("kahi-ui-%d.sock", os.Getpid()))
	t.Cleanup(func() { _ = os.Remove(sockPath) })
	if err := srv.StartUnix(sockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
		},
	}}
	resp, err := client.Get("http://unix/ui/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ui:/ui/" {
		t.Errorf("GET /ui/ over the socket without credentials: status %d, body %q", resp.StatusCode, body)
	}
}

// --- Stream subscription order (FUNC-079, ADR-029 point 7) ---

// flushProbe records, at the first Flush, the bus subscriber count for one
// event type, then cancels the request so the streaming handler returns.
type flushProbe struct {
	*httptest.ResponseRecorder
	bus    *events.Bus
	et     events.EventType
	cancel context.CancelFunc
	count  int
	seen   bool
}

func (f *flushProbe) Flush() {
	if !f.seen {
		f.seen = true
		f.count = f.bus.SubscriberCount(f.et)
		f.cancel()
	}
	f.ResponseRecorder.Flush()
}

func probeFirstFlush(t *testing.T, path string, et events.EventType) int {
	t.Helper()
	srv := newTestServer(newBulkProcesses(), &mockDaemonInfo{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &flushProbe{ResponseRecorder: httptest.NewRecorder(), bus: srv.bus, et: et, cancel: cancel}
	done := make(chan struct{})
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	req.SetBasicAuth("admin", "secret")
	go func() {
		srv.handler.ServeHTTP(probe, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: handler did not return after its context was cancelled", path)
	}
	if !probe.seen {
		t.Fatalf("%s: handler never flushed", path)
	}
	return probe.count
}

func TestStreamLogSubscribesBeforeFlush(t *testing.T) {
	if n := probeFirstFlush(t, "/api/v1/processes/web/log/stdout/stream", events.ProcessLogStdout); n != 1 {
		t.Errorf("subscribers at the first flush = %d, want 1: subscribe before flushing headers", n)
	}
}

func TestEventStreamSubscribesBeforeFlush(t *testing.T) {
	if n := probeFirstFlush(t, "/api/v1/events/stream", events.ProcessStateRunning); n != 1 {
		t.Errorf("subscribers at the first flush = %d, want 1: subscribe before flushing headers", n)
	}
}

// --- Bounded shutdown (FUNC-084) ---

func TestStopClosesOpenStreams(t *testing.T) {
	srv := newTestServer(newBulkProcesses(), &mockDaemonInfo{}, nil)
	if err := srv.StartTCP("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+srv.TCPAddr()+"/api/v1/events/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("admin", "secret")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	readDone := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(readDone) }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = srv.Stop(ctx)
	if took := time.Since(start); took > time.Second {
		t.Errorf("Stop took %v with an open stream, want under 1s", took)
	}
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Error("the open SSE connection was not closed by Stop")
	}
}
