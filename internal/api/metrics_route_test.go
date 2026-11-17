package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/events"
)

func metricsServer(enabled bool, h http.Handler) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := Config{Username: "admin", Password: testPassHash, MetricsEnabled: enabled, Metrics: h}
	return NewServer(cfg, &mockProcessManager{}, &mockGroupManager{}, &mockConfigManager{}, &mockDaemonInfo{}, events.NewBus(logger), logger)
}

func getMetrics(s *Server, tcp, auth bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.RemoteAddr = ""
	if tcp {
		req.RemoteAddr = "127.0.0.1:12345"
	}
	if auth {
		req.SetBasicAuth("admin", "secret")
	}
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, req)
	return w
}

// FUNC-060: /metrics states and auth (TCP only).
func TestMetricsRoute(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "# TYPE x gauge\n") })
	s := metricsServer(true, ok)
	if w := getMetrics(s, false, false); w.Code != 200 || !strings.Contains(w.Body.String(), "# TYPE") {
		t.Errorf("unix: %d %s", w.Code, w.Body)
	}
	if w := getMetrics(s, true, false); w.Code != http.StatusUnauthorized {
		t.Errorf("tcp without credentials: %d", w.Code)
	}
	if w := getMetrics(s, true, true); w.Code != 200 {
		t.Errorf("tcp with credentials: %d", w.Code)
	}
	if w := getMetrics(metricsServer(false, nil), false, false); w.Code != 404 || !strings.Contains(w.Body.String(), `"error":"metrics not enabled"`) {
		t.Errorf("disabled: %d %s", w.Code, w.Body)
	}
	// The collector could not be built (ADR-049).
	if w := getMetrics(metricsServer(true, nil), false, false); w.Code != 503 || !strings.Contains(w.Body.String(), `"error":"metrics unavailable"`) {
		t.Errorf("unavailable: %d %s", w.Code, w.Body)
	}
}
