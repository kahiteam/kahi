package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type mockLister struct {
	processes []ProcessView
}

func (m *mockLister) ListWeb() []ProcessView {
	return m.processes
}

func newTestHandler(t *testing.T, lister ProcessLister) *Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	h, err := NewHandler(lister, Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestStatusPage(t *testing.T) {
	lister := &mockLister{
		processes: []ProcessView{
			{Name: "web", Group: "default", State: "RUNNING", StateLower: "running", PID: 1234, UptimeStr: "1h 5m"},
			{Name: "worker", Group: "default", State: "STOPPED", StateLower: "stopped"},
		},
	}
	h := newTestHandler(t, lister)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body := w.Body.String()

	// Process names present.
	if !strings.Contains(body, "web") {
		t.Error("missing process name 'web'")
	}
	if !strings.Contains(body, "worker") {
		t.Error("missing process name 'worker'")
	}

	// State elements present.
	if !strings.Contains(body, "state-running") {
		t.Error("missing state-running class")
	}
	if !strings.Contains(body, "state-stopped") {
		t.Error("missing state-stopped class")
	}

	// Action buttons present.
	if !strings.Contains(body, "Start") {
		t.Error("missing Start button")
	}
	if !strings.Contains(body, "Stop") {
		t.Error("missing Stop button")
	}
	if !strings.Contains(body, "Restart") {
		t.Error("missing Restart button")
	}
	if !strings.Contains(body, "Tail Stdout") {
		t.Error("missing Tail Stdout link")
	}
	if !strings.Contains(body, "Tail Stderr") {
		t.Error("missing Tail Stderr link")
	}

	// SSE JavaScript is loaded via app.js.
	if !strings.Contains(body, "app.js") {
		t.Error("missing app.js script reference")
	}

	// Viewport meta tag for responsive layout.
	if !strings.Contains(body, "viewport") {
		t.Error("missing viewport meta tag")
	}

	// PID and uptime columns.
	if !strings.Contains(body, "1234") {
		t.Error("missing PID value")
	}
	if !strings.Contains(body, "1h 5m") {
		t.Error("missing uptime value")
	}
}

func TestStatusPageNoJS(t *testing.T) {
	lister := &mockLister{
		processes: []ProcessView{
			{Name: "web", State: "RUNNING", StateLower: "running"},
		},
	}
	h := newTestHandler(t, lister)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	body := w.Body.String()

	// Verify process data is in HTML even without JS execution.
	if !strings.Contains(body, "web") {
		t.Error("process data should be in HTML without JS")
	}
}

func TestLogViewerPage(t *testing.T) {
	h := newTestHandler(t, &mockLister{})

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/log/web/stdout", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("status = %d, want 200", w.Result().StatusCode)
	}

	body := w.Body.String()

	// Log page should contain process name and stream.
	if !strings.Contains(body, "web") {
		t.Error("missing process name in log page")
	}
	if !strings.Contains(body, "stdout") {
		t.Error("missing stream name in log page")
	}

	// EventSource for SSE.
	if !strings.Contains(body, "EventSource") {
		t.Error("missing EventSource in log page")
	}

	// ANSI rendering present.
	if !strings.Contains(body, "renderAnsi") {
		t.Error("missing ANSI rendering function")
	}

	// Auto-scroll checkbox.
	if !strings.Contains(body, "autoscroll") {
		t.Error("missing auto-scroll control")
	}
}

func TestLogViewerStderr(t *testing.T) {
	h := newTestHandler(t, &mockLister{})

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/log/web/stderr", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("status = %d, want 200", w.Result().StatusCode)
	}

	body := w.Body.String()
	if !strings.Contains(body, "stderr") {
		t.Error("missing stderr stream name")
	}
	if !strings.Contains(body, "web") {
		t.Error("missing process name on stderr page")
	}
}

func TestLogViewerInvalidStream(t *testing.T) {
	h := newTestHandler(t, &mockLister{})

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/log/web/invalid", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Result().StatusCode != 404 {
		t.Fatalf("status = %d, want 404", w.Result().StatusCode)
	}
}

func TestStaticAssetsServed(t *testing.T) {
	h := newTestHandler(t, &mockLister{})

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// CSS file.
	req := httptest.NewRequest("GET", "/static/style.css", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("CSS status = %d, want 200", w.Result().StatusCode)
	}
	ct := w.Result().Header.Get("Content-Type")
	if !strings.Contains(ct, "text/css") {
		t.Errorf("CSS Content-Type = %q, want text/css", ct)
	}

	// Caching headers.
	if w.Result().Header.Get("Cache-Control") == "" {
		t.Error("missing Cache-Control header")
	}
	if w.Result().Header.Get("ETag") == "" {
		t.Error("missing ETag header")
	}

	// JS file.
	req = httptest.NewRequest("GET", "/static/app.js", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("JS status = %d, want 200", w.Result().StatusCode)
	}
	ct = w.Result().Header.Get("Content-Type")
	if !strings.Contains(ct, "javascript") {
		t.Errorf("JS Content-Type = %q, want javascript", ct)
	}
}

func TestStaticDirFallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	h, err := NewHandler(&mockLister{}, Config{StaticDir: "/nonexistent/path"}, logger)
	if err != nil {
		t.Fatal(err)
	}
	// Should fall back to embedded assets without error.
	if h.staticFS == nil {
		t.Error("staticFS should not be nil after fallback")
	}
}

func TestCSSStateColors(t *testing.T) {
	h := newTestHandler(t, &mockLister{})

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/static/style.css", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	css := w.Body.String()

	requiredClasses := []string{
		".state-running",
		".state-stopped",
		".state-fatal",
		".state-starting",
		".state-stopping",
	}
	for _, cls := range requiredClasses {
		if !strings.Contains(css, cls) {
			t.Errorf("CSS missing class %q", cls)
		}
	}
}

func TestCSSResponsiveMediaQuery(t *testing.T) {
	h := newTestHandler(t, &mockLister{})

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/static/style.css", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	css := w.Body.String()

	if !strings.Contains(css, "max-width: 768px") {
		t.Error("CSS missing responsive media query for 768px")
	}
}

func TestCSSNoExternalURLs(t *testing.T) {
	h := newTestHandler(t, &mockLister{})

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/static/style.css", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	css := w.Body.String()
	if strings.Contains(css, "http://") || strings.Contains(css, "https://") {
		t.Error("CSS should not contain external URLs")
	}
}

func TestCSSMonospaceLogViewer(t *testing.T) {
	h := newTestHandler(t, &mockLister{})

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/static/style.css", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	css := w.Body.String()
	if !strings.Contains(css, "monospace") {
		t.Error("CSS missing monospace font for log viewer")
	}
}

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		seconds int64
		want    string
	}{
		{0, "0m"},
		{300, "5m"},
		{3660, "1h 1m"},
		{90060, "1d 1h 1m"},
	}
	for _, tt := range tests {
		got := FormatUptime(tt.seconds)
		if got != tt.want {
			t.Errorf("FormatUptime(%d) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}
