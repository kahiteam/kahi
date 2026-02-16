package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewCollector(t *testing.T) {
	c := New()
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
}

func TestMetricsHandler(t *testing.T) {
	c := New()
	handler := c.Handler()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body, _ := io.ReadAll(w.Body)
	content := string(body)

	// Should contain Go runtime metrics.
	if !strings.Contains(content, "go_goroutines") {
		t.Fatal("expected go_goroutines metric")
	}
}

func TestProcessStateMetric(t *testing.T) {
	c := New()
	c.SetProcessState("web", "web", 20) // RUNNING = 20

	body := scrape(t, c)
	if !strings.Contains(body, `kahi_process_state{group="web",name="web"} 20`) {
		t.Fatalf("expected process state metric, got:\n%s", body)
	}
}

func TestProcessStartCounter(t *testing.T) {
	c := New()
	c.IncProcessStart("web")
	c.IncProcessStart("web")
	c.IncProcessStart("web")
	c.IncProcessStart("web")
	c.IncProcessStart("web")

	body := scrape(t, c)
	if !strings.Contains(body, `kahi_process_start_total{name="web"} 5`) {
		t.Fatalf("expected start_total=5, got:\n%s", body)
	}
}

func TestProcessExitCounter(t *testing.T) {
	c := New()
	c.IncProcessExit("web", false)
	c.IncProcessExit("web", true)
	c.IncProcessExit("web", false)

	body := scrape(t, c)
	if !strings.Contains(body, `kahi_process_exit_total{expected="false",name="web"} 2`) {
		t.Fatalf("expected exit_total unexpected=2, got:\n%s", body)
	}
	if !strings.Contains(body, `kahi_process_exit_total{expected="true",name="web"} 1`) {
		t.Fatalf("expected exit_total expected=1, got:\n%s", body)
	}
}

func TestSupervisorUptime(t *testing.T) {
	c := New()
	c.SetSupervisorUptime(3600.5)

	body := scrape(t, c)
	if !strings.Contains(body, "kahi_supervisor_uptime_seconds 3600.5") {
		t.Fatalf("expected uptime metric, got:\n%s", body)
	}
}

func TestProcessCountPerState(t *testing.T) {
	c := New()
	c.SetProcessCount("running", 5)
	c.SetProcessCount("stopped", 2)

	body := scrape(t, c)
	if !strings.Contains(body, `kahi_supervisor_processes{state="running"} 5`) {
		t.Fatalf("expected running=5, got:\n%s", body)
	}
	if !strings.Contains(body, `kahi_supervisor_processes{state="stopped"} 2`) {
		t.Fatalf("expected stopped=2, got:\n%s", body)
	}
}

func TestConfigReloadCounters(t *testing.T) {
	c := New()
	c.IncConfigReload()
	c.IncConfigReload()
	c.IncConfigReloadError()

	body := scrape(t, c)
	if !strings.Contains(body, "kahi_supervisor_config_reload_total 2") {
		t.Fatalf("expected reload_total=2, got:\n%s", body)
	}
	if !strings.Contains(body, "kahi_supervisor_config_reload_errors_total 1") {
		t.Fatalf("expected reload_errors=1, got:\n%s", body)
	}
}

func TestBuildInfo(t *testing.T) {
	c := New()
	c.SetBuildInfo("1.0.0", "go1.26.0", "true")

	body := scrape(t, c)
	if !strings.Contains(body, `kahi_info{fips="true",go_version="go1.26.0",version="1.0.0"} 1`) {
		t.Fatalf("expected build info metric, got:\n%s", body)
	}
}

func TestRemoveProcess(t *testing.T) {
	c := New()
	c.SetProcessState("web", "web", 20)
	c.IncProcessStart("web")
	c.IncProcessExit("web", false)
	c.SetProcessUptime("web", 100)

	c.RemoveProcess("web", "web")

	body := scrape(t, c)
	if strings.Contains(body, `name="web"`) {
		t.Fatalf("expected web metrics to be removed, got:\n%s", body)
	}
}

func TestMetricNamingConventions(t *testing.T) {
	c := New()
	// Initialize all metrics so they appear in output.
	c.SetProcessState("test", "test", 0)
	c.IncProcessStart("test")
	c.IncProcessExit("test", false)
	c.SetProcessUptime("test", 1)
	c.SetSupervisorUptime(1)
	c.SetProcessCount("running", 1)
	c.IncConfigReload()
	c.IncConfigReloadError()
	c.SetBuildInfo("dev", "go1.26", "false")

	body := scrape(t, c)

	// All metric names should be snake_case.
	metricNames := []string{
		"kahi_process_state",
		"kahi_process_start_total",
		"kahi_process_exit_total",
		"kahi_process_uptime_seconds",
		"kahi_supervisor_uptime_seconds",
		"kahi_supervisor_processes",
		"kahi_supervisor_config_reload_total",
		"kahi_supervisor_config_reload_errors_total",
		"kahi_info",
	}
	for _, name := range metricNames {
		if !strings.Contains(body, name) {
			t.Errorf("expected metric %s in output", name)
		}
	}
}

func scrape(t *testing.T, c *Collector) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics scrape failed: %d", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	return string(body)
}
