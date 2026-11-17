package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/metrics"
	"github.com/kahiteam/kahi/internal/supervisor"
)

func testSupervisor(t *testing.T) *supervisor.Supervisor {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return supervisor.New(supervisor.SupervisorConfig{Config: &config.Config{}, Logger: logger})
}

func TestBuildMetrics(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if h := buildMetrics(false, testSupervisor(t), logger); h != nil {
		t.Error("disabled metrics built a handler")
	}
	h := buildMetrics(true, testSupervisor(t), logger)
	if h == nil {
		t.Fatal("no handler")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	for _, want := range []string{"kahi_info{", "kahi_supervisor_uptime_seconds", `kahi_supervisor_processes{state="running"} 0`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("scrape lacks %q", want)
		}
	}
}

// A collector that cannot be built leaves no handler (the API answers 503)
// and the supervisor usable (FUNC-060 step 10, ADR-049).
func TestBuildMetricsFailure(t *testing.T) {
	old := newMetrics
	t.Cleanup(func() { newMetrics = old })
	newMetrics = func(metrics.Source, metrics.BuildInfo) (*metrics.Collector, error) { return nil, errors.New("boom") }
	sup := testSupervisor(t)
	if h := buildMetrics(true, sup, slog.New(slog.NewTextHandler(io.Discard, nil))); h != nil {
		t.Fatal("handler despite a failed collector")
	}
	if sup.Manager() == nil {
		t.Fatal("supervisor unusable")
	}
}
