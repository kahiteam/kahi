package main

import (
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/kahiteam/kahi/internal/metrics"
	"github.com/kahiteam/kahi/internal/process"
	"github.com/kahiteam/kahi/internal/supervisor"
	"github.com/kahiteam/kahi/internal/version"
)

// newMetrics is the collector constructor, replaced in tests.
var newMetrics = metrics.New

// buildMetrics builds the /metrics handler when [server.metrics] is on, and
// hooks the counters into the manager and the reload path (FUNC-060..062,
// ADR-049). A collector that cannot be built is logged and leaves /metrics
// at 503; the daemon keeps supervising.
func buildMetrics(enabled bool, sup *supervisor.Supervisor, logger *slog.Logger) http.Handler {
	if !enabled {
		return nil
	}
	goVersion := version.GoVersion
	if goVersion == "" {
		goVersion = runtime.Version()
	}
	c, err := newMetrics(metricsSource{sup: sup}, metrics.BuildInfo{Version: version.Version, GoVersion: goVersion, FIPS: version.FIPS})
	if err != nil {
		logger.Error("metrics unavailable", "error", err)
		return nil
	}
	sup.Manager().SetMetrics(c)
	sup.SetReloadRecorder(c)
	return c.Handler()
}

// metricsSource answers the scrape-time questions from the supervisor.
type metricsSource struct{ sup *supervisor.Supervisor }

func (s metricsSource) Processes() []metrics.ProcessSnapshot {
	procs := s.sup.Manager().Processes()
	out := make([]metrics.ProcessSnapshot, 0, len(procs))
	for _, p := range procs {
		st := p.State()
		snap := metrics.ProcessSnapshot{Name: p.Name(), Group: p.Group(), State: st.String()}
		if st == process.Running {
			snap.Uptime = time.Duration(p.Uptime()) * time.Second
		}
		out = append(out, snap)
	}
	return out
}

func (s metricsSource) PoolDrops() map[string]uint64    { return s.sup.Manager().PoolDrops() }
func (s metricsSource) Breakers() map[string]bool       { return s.sup.WebhookBreakers() }
func (s metricsSource) SupervisorUptime() time.Duration { return s.sup.Uptime() }
