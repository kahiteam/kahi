package main

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/kahiteam/kahi/internal/api"
	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/web"
)

// uiConstructor builds the Web UI handler; web.NewHandler in the daemon, a
// failing stand-in in tests.
type uiConstructor func(web.ProcessLister, web.Config, *slog.Logger) (*web.Handler, error)

// buildUIHandler returns the handler to mount under /ui/ (FUNC-084, ADR-029).
// When the UI is disabled it returns an untyped nil: a nil *web.Handler
// inside the interface would be non-nil and get mounted. When the handler
// cannot be built, supervision carries on and /ui/ answers 503 (Principle 5).
func buildUIHandler(cfg config.WebServerConfig, pm api.ProcessManager, newHandler uiConstructor, logger *slog.Logger) http.Handler {
	if !cfg.Enabled {
		return nil
	}
	h, err := newHandler(uiLister{pm: pm}, web.Config{StaticDir: cfg.StaticDir}, logger)
	if err != nil {
		logger.Error("web UI unavailable", "error", err)
		return uiUnavailable()
	}
	return h
}

// uiUnavailable answers every UI request with 503, with the same security
// headers the UI itself sends.
func uiUnavailable() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		web.SetSecurityHeaders(w.Header())
		http.Error(w, "web UI unavailable", http.StatusServiceUnavailable)
	})
}

// uiLister adapts the API's process list to the Web UI's view.
type uiLister struct {
	pm api.ProcessManager
}

func (l uiLister) ListWeb() []web.ProcessView {
	infos := l.pm.List()
	views := make([]web.ProcessView, len(infos))
	for i, p := range infos {
		views[i] = web.ProcessView{
			Name:        p.Name,
			Group:       p.Group,
			State:       p.State,
			StateLower:  stateClassName(p.State),
			PID:         p.PID,
			Uptime:      p.Uptime,
			UptimeStr:   web.FormatUptime(p.Uptime),
			Description: p.Description,
			ExitStatus:  p.ExitStatus,
		}
	}
	return views
}

// knownStates are the process states the UI has a CSS class for.
var knownStates = map[string]bool{
	"STOPPED": true, "STARTING": true, "RUNNING": true, "BACKOFF": true,
	"STOPPING": true, "EXITED": true, "FATAL": true,
}

// stateClassName lowercases a known state for its CSS class and maps anything
// else, such as UNKNOWN(9), to "unknown".
func stateClassName(state string) string {
	if knownStates[state] {
		return strings.ToLower(state)
	}
	return "unknown"
}
