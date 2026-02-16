// Package metrics collects and exposes Prometheus metrics for Kahi.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Collector holds all Kahi-specific Prometheus metrics.
type Collector struct {
	registry *prometheus.Registry

	// Per-process metrics.
	ProcessState      *prometheus.GaugeVec
	ProcessStartTotal *prometheus.CounterVec
	ProcessExitTotal  *prometheus.CounterVec
	ProcessUptime     *prometheus.GaugeVec

	// Supervisor-level metrics.
	SupervisorUptime       prometheus.Gauge
	SupervisorProcesses    *prometheus.GaugeVec
	ConfigReloadTotal      prometheus.Counter
	ConfigReloadErrorTotal prometheus.Counter
	BuildInfo              *prometheus.GaugeVec
}

// New creates and registers all Kahi metrics.
func New() *Collector {
	reg := prometheus.NewRegistry()

	// Register default Go runtime metrics.
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	c := &Collector{
		registry: reg,

		ProcessState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kahi_process_state",
				Help: "Current state of a managed process (numeric state code).",
			},
			[]string{"name", "group"},
		),

		ProcessStartTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "kahi_process_start_total",
				Help: "Total number of times a process has been started.",
			},
			[]string{"name"},
		),

		ProcessExitTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "kahi_process_exit_total",
				Help: "Total number of process exits.",
			},
			[]string{"name", "expected"},
		),

		ProcessUptime: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kahi_process_uptime_seconds",
				Help: "Uptime of a managed process in seconds.",
			},
			[]string{"name"},
		),

		SupervisorUptime: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "kahi_supervisor_uptime_seconds",
				Help: "Uptime of the Kahi supervisor in seconds.",
			},
		),

		SupervisorProcesses: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kahi_supervisor_processes",
				Help: "Number of processes per state.",
			},
			[]string{"state"},
		),

		ConfigReloadTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "kahi_supervisor_config_reload_total",
				Help: "Total number of config reloads.",
			},
		),

		ConfigReloadErrorTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "kahi_supervisor_config_reload_errors_total",
				Help: "Total number of failed config reloads.",
			},
		),

		BuildInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kahi_info",
				Help: "Build information about Kahi.",
			},
			[]string{"version", "go_version", "fips"},
		),
	}

	reg.MustRegister(
		c.ProcessState,
		c.ProcessStartTotal,
		c.ProcessExitTotal,
		c.ProcessUptime,
		c.SupervisorUptime,
		c.SupervisorProcesses,
		c.ConfigReloadTotal,
		c.ConfigReloadErrorTotal,
		c.BuildInfo,
	)

	return c
}

// Handler returns an http.Handler that serves the /metrics endpoint.
func (c *Collector) Handler() http.Handler {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{})
}

// SetBuildInfo sets the constant build info gauge.
func (c *Collector) SetBuildInfo(version, goVersion, fips string) {
	c.BuildInfo.WithLabelValues(version, goVersion, fips).Set(1)
}

// SetProcessState updates the state gauge for a process.
func (c *Collector) SetProcessState(name, group string, stateCode int) {
	c.ProcessState.WithLabelValues(name, group).Set(float64(stateCode))
}

// IncProcessStart increments the start counter for a process.
func (c *Collector) IncProcessStart(name string) {
	c.ProcessStartTotal.WithLabelValues(name).Inc()
}

// IncProcessExit increments the exit counter for a process.
func (c *Collector) IncProcessExit(name string, expected bool) {
	label := "false"
	if expected {
		label = "true"
	}
	c.ProcessExitTotal.WithLabelValues(name, label).Inc()
}

// SetProcessUptime sets the uptime gauge for a process.
func (c *Collector) SetProcessUptime(name string, seconds float64) {
	c.ProcessUptime.WithLabelValues(name).Set(seconds)
}

// SetSupervisorUptime sets the supervisor uptime gauge.
func (c *Collector) SetSupervisorUptime(seconds float64) {
	c.SupervisorUptime.Set(seconds)
}

// SetProcessCount sets the count of processes in a given state.
func (c *Collector) SetProcessCount(state string, count int) {
	c.SupervisorProcesses.WithLabelValues(state).Set(float64(count))
}

// IncConfigReload increments the config reload counter.
func (c *Collector) IncConfigReload() {
	c.ConfigReloadTotal.Inc()
}

// IncConfigReloadError increments the config reload error counter.
func (c *Collector) IncConfigReloadError() {
	c.ConfigReloadErrorTotal.Inc()
}

// RemoveProcess cleans up metrics for a removed process.
func (c *Collector) RemoveProcess(name, group string) {
	c.ProcessState.DeleteLabelValues(name, group)
	c.ProcessStartTotal.DeleteLabelValues(name)
	c.ProcessExitTotal.DeletePartialMatch(prometheus.Labels{"name": name})
	c.ProcessUptime.DeleteLabelValues(name)
}
