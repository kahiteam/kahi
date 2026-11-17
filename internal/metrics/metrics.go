// Package metrics collects and exposes Prometheus metrics for Kahi
// (FUNC-060..062, ADR-049).
//
// Counters (starts, exits, reloads) are incremented directly by the process
// manager and the reload path, never through the event bus, so a full queue
// can never lose a count. Values that describe the present (process state
// and uptime, per-state counts, pool drops, webhook breakers) are read from a
// Source at scrape time, so a removed process has no stale series.
package metrics

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ProcessSnapshot is one managed process at scrape time.
type ProcessSnapshot struct {
	Name   string
	Group  string
	State  string        // STOPPED, STARTING, RUNNING, BACKOFF, STOPPING, EXITED, FATAL
	Uptime time.Duration // since the process entered RUNNING; 0 otherwise
}

// Source answers the scrape-time questions.
type Source interface {
	Processes() []ProcessSnapshot
	PoolDrops() map[string]uint64 // event listener pool name -> events dropped
	Breakers() map[string]bool    // webhook name -> circuit open
	SupervisorUptime() time.Duration
}

// BuildInfo labels kahi_info.
type BuildInfo struct {
	Version   string
	GoVersion string
	FIPS      string
}

// stateCodes are the kahi_process_state values (FUNC-061).
var stateCodes = map[string]float64{
	"STOPPED":  0,
	"STARTING": 10,
	"RUNNING":  20,
	"BACKOFF":  30,
	"STOPPING": 40,
	"EXITED":   100,
	"FATAL":    200,
}

// States lists the process states kahi_supervisor_processes reports, each
// with a series even at zero.
var States = []string{"STOPPED", "STARTING", "RUNNING", "BACKOFF", "STOPPING", "EXITED", "FATAL"}

// Collector holds Kahi's metrics registry.
type Collector struct {
	registry     *prometheus.Registry
	startTotal   *prometheus.CounterVec
	exitTotal    *prometheus.CounterVec
	reloadTotal  prometheus.Counter
	reloadErrors prometheus.Counter
}

// register is the registration seam; tests replace it to prove that a
// collector failure leaves the daemon running with /metrics at 503.
var register = func(reg *prometheus.Registry, c prometheus.Collector) error {
	return reg.Register(c)
}

// New builds the registry: Go and process collectors, Kahi's counters,
// kahi_info and the scrape-time collector over src. It returns an error
// instead of panicking, so the daemon keeps supervising (ADR-049).
func New(src Source, info BuildInfo) (*Collector, error) {
	reg := prometheus.NewRegistry()
	c := &Collector{
		registry: reg,
		startTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kahi_process_start_total",
			Help: "Spawns of a managed process: manual starts, autorestarts and backoff retries.",
		}, []string{"name"}),
		exitTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kahi_process_exit_total",
			Help: "Exits of a managed process; expected is true after a stop or an exit code in exitcodes from RUNNING.",
		}, []string{"name", "expected"}),
		reloadTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kahi_supervisor_config_reload_total",
			Help: "Config reload attempts, applied or rejected (dry runs excluded).",
		}),
		reloadErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kahi_supervisor_config_reload_errors_total",
			Help: "Config reload attempts that were rejected.",
		}),
	}
	infoGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kahi_info",
		Help: "Kahi build information; always 1.",
	}, []string{"version", "go_version", "fips"})
	infoGauge.WithLabelValues(info.Version, info.GoVersion, info.FIPS).Set(1)

	for _, col := range []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		c.startTotal, c.exitTotal, c.reloadTotal, c.reloadErrors, infoGauge,
		&scrapeCollector{src: src},
	} {
		if err := register(reg, col); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Handler serves the registry in the Prometheus text format.
func (c *Collector) Handler() http.Handler {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{})
}

// ProcessStarted counts one spawn of name.
func (c *Collector) ProcessStarted(name string) {
	c.startTotal.WithLabelValues(name).Inc()
}

// ProcessExited counts one exit of name.
func (c *Collector) ProcessExited(name string, expected bool) {
	e := "false"
	if expected {
		e = "true"
	}
	c.exitTotal.WithLabelValues(name, e).Inc()
}

// RemoveProcess deletes the counters of a process that is gone from the
// config. Counters of a recreated process (a changed program) are kept.
func (c *Collector) RemoveProcess(name string) {
	c.startTotal.DeletePartialMatch(prometheus.Labels{"name": name})
	c.exitTotal.DeletePartialMatch(prometheus.Labels{"name": name})
}

// ReloadAttempt counts one reload attempt; failed counts it as an error too.
func (c *Collector) ReloadAttempt(failed bool) {
	c.reloadTotal.Inc()
	if failed {
		c.reloadErrors.Inc()
	}
}

var (
	descState    = prometheus.NewDesc("kahi_process_state", "Current state of a managed process: STOPPED 0, STARTING 10, RUNNING 20, BACKOFF 30, STOPPING 40, EXITED 100, FATAL 200, unknown -1.", []string{"name", "group"}, nil)
	descUptime   = prometheus.NewDesc("kahi_process_uptime_seconds", "Seconds since the process entered RUNNING; 0 when not RUNNING.", []string{"name"}, nil)
	descSupUp    = prometheus.NewDesc("kahi_supervisor_uptime_seconds", "Seconds since the supervisor started.", nil, nil)
	descPerState = prometheus.NewDesc("kahi_supervisor_processes", "Managed processes per state.", []string{"state"}, nil)
	descDrops    = prometheus.NewDesc("kahi_event_buffer_drops_total", "Events an event listener pool dropped because its buffer was full.", []string{"pool"}, nil)
	descBreaker  = prometheus.NewDesc("kahi_webhook_circuit_breaker", "Webhook circuit breaker status: the current status is 1, the other 0.", []string{"webhook", "status"}, nil)
)

// scrapeCollector reads the present from a Source at each scrape.
type scrapeCollector struct{ src Source }

func (s *scrapeCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{descState, descUptime, descSupUp, descPerState, descDrops, descBreaker} {
		ch <- d
	}
}

func (s *scrapeCollector) Collect(ch chan<- prometheus.Metric) {
	perState := make(map[string]int, len(States))
	for _, p := range s.src.Processes() {
		code, ok := stateCodes[p.State]
		if !ok {
			code = -1
		}
		ch <- prometheus.MustNewConstMetric(descState, prometheus.GaugeValue, code, p.Name, p.Group)
		up := 0.0
		if p.State == "RUNNING" {
			up = p.Uptime.Seconds()
		}
		ch <- prometheus.MustNewConstMetric(descUptime, prometheus.GaugeValue, up, p.Name)
		perState[p.State]++
	}
	for _, st := range States {
		ch <- prometheus.MustNewConstMetric(descPerState, prometheus.GaugeValue, float64(perState[st]), strings.ToLower(st))
	}
	ch <- prometheus.MustNewConstMetric(descSupUp, prometheus.GaugeValue, s.src.SupervisorUptime().Seconds())
	for pool, n := range s.src.PoolDrops() {
		ch <- prometheus.MustNewConstMetric(descDrops, prometheus.CounterValue, float64(n), pool)
	}
	names := make([]string, 0)
	breakers := s.src.Breakers()
	for name := range breakers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		open, closed := 0.0, 1.0
		if breakers[name] {
			open, closed = 1, 0
		}
		ch <- prometheus.MustNewConstMetric(descBreaker, prometheus.GaugeValue, open, name, "open")
		ch <- prometheus.MustNewConstMetric(descBreaker, prometheus.GaugeValue, closed, name, "closed")
	}
}
