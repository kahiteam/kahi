package metrics

import (
	"errors"
	"io"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type fakeSource struct {
	procs    []ProcessSnapshot
	drops    map[string]uint64
	breakers map[string]bool
	uptime   time.Duration
}

func (f *fakeSource) Processes() []ProcessSnapshot    { return f.procs }
func (f *fakeSource) PoolDrops() map[string]uint64    { return f.drops }
func (f *fakeSource) Breakers() map[string]bool       { return f.breakers }
func (f *fakeSource) SupervisorUptime() time.Duration { return f.uptime }

func newTest(t *testing.T, src *fakeSource) *Collector {
	t.Helper()
	c, err := New(src, BuildInfo{Version: "1.2.3", GoVersion: "go1.27.4", FIPS: "false"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func scrape(t *testing.T, c *Collector) string {
	t.Helper()
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	b, _ := io.ReadAll(w.Body)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	return string(b)
}

func wantLines(t *testing.T, body string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains(body, l+"\n") {
			t.Errorf("missing %q", l)
		}
	}
}

func TestScrapeValues(t *testing.T) {
	src := &fakeSource{
		procs: []ProcessSnapshot{
			{Name: "web", Group: "web", State: "RUNNING", Uptime: 90 * time.Second},
			{Name: "job", Group: "batch", State: "STARTING", Uptime: 5 * time.Second},
			{Name: "odd", Group: "odd", State: "WEIRD"},
		},
		drops:    map[string]uint64{"l": 4},
		breakers: map[string]bool{"t": false, "u": true},
		uptime:   12 * time.Second,
	}
	c := newTest(t, src)
	c.ProcessStarted("web")
	c.ProcessStarted("web")
	c.ProcessExited("web", false)
	c.ProcessExited("web", true)
	c.ReloadAttempt(false)
	c.ReloadAttempt(true)
	body := scrape(t, c)
	wantLines(t, body,
		`kahi_process_state{group="web",name="web"} 20`,
		`kahi_process_state{group="batch",name="job"} 10`,
		`kahi_process_state{group="odd",name="odd"} -1`,
		`kahi_process_uptime_seconds{name="web"} 90`,
		`kahi_process_uptime_seconds{name="job"} 0`,
		`kahi_process_start_total{name="web"} 2`,
		`kahi_process_exit_total{expected="false",name="web"} 1`,
		`kahi_process_exit_total{expected="true",name="web"} 1`,
		`kahi_supervisor_uptime_seconds 12`,
		`kahi_supervisor_processes{state="running"} 1`,
		`kahi_supervisor_processes{state="fatal"} 0`,
		`kahi_supervisor_config_reload_total 2`,
		`kahi_supervisor_config_reload_errors_total 1`,
		`kahi_info{fips="false",go_version="go1.27.4",version="1.2.3"} 1`,
		`kahi_event_buffer_drops_total{pool="l"} 4`,
		`kahi_webhook_circuit_breaker{status="closed",webhook="t"} 1`,
		`kahi_webhook_circuit_breaker{status="open",webhook="t"} 0`,
		`kahi_webhook_circuit_breaker{status="open",webhook="u"} 1`,
	)
	if !strings.Contains(body, "go_goroutines") {
		t.Error("no Go collector")
	}
}

// A removed process's counters go; its state series goes with the source.
func TestRemoveProcess(t *testing.T) {
	src := &fakeSource{procs: []ProcessSnapshot{{Name: "p", Group: "p", State: "STOPPED"}}}
	c := newTest(t, src)
	c.ProcessStarted("p")
	c.ProcessExited("p", false)
	c.ProcessStarted("q")
	src.procs = nil
	c.RemoveProcess("p")
	body := scrape(t, c)
	if strings.Contains(body, `name="p"`) {
		t.Errorf("series for p left:\n%s", body)
	}
	if !strings.Contains(body, `kahi_process_start_total{name="q"} 1`) {
		t.Error("q removed too")
	}
}

// Counter names end in _total; every kahi_ name is snake_case (FUNC-060).
func TestNamingConventions(t *testing.T) {
	c := newTest(t, &fakeSource{procs: []ProcessSnapshot{{Name: "a", Group: "a", State: "RUNNING"}}, drops: map[string]uint64{"l": 0}, breakers: map[string]bool{"t": false}})
	c.ProcessStarted("a")
	c.ProcessExited("a", true)
	body := scrape(t, c)
	typeRe := regexp.MustCompile(`(?m)^# TYPE (\S+) (\S+)$`)
	for _, m := range typeRe.FindAllStringSubmatch(body, -1) {
		name, typ := m[1], m[2]
		if !strings.HasPrefix(name, "kahi_") {
			continue
		}
		if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(name) {
			t.Errorf("%s is not snake_case", name)
		}
		if typ == "counter" && !strings.HasSuffix(name, "_total") {
			t.Errorf("counter %s does not end in _total", name)
		}
	}
}

// A registration failure is an error, not a panic (ADR-049).
func TestNewReportsRegistrationFailure(t *testing.T) {
	old := register
	t.Cleanup(func() { register = old })
	register = func(*prometheus.Registry, prometheus.Collector) error { return errors.New("boom") }
	if c, err := New(&fakeSource{}, BuildInfo{}); err == nil || c != nil {
		t.Fatalf("New = %v, %v; want an error", c, err)
	}
}
