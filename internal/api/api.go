// Package api exposes the Kahi control API over Unix socket and optional TCP.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
	"github.com/kahiteam/kahi/internal/sockutil"
	"golang.org/x/crypto/bcrypt"
)

// chmodSocket is a seam over os.Chmod so the fail-closed path is testable.
var chmodSocket = os.Chmod

// ProcessInfo describes the runtime state of a managed process.
type ProcessInfo struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	State       string `json:"state"`
	StateCode   int    `json:"statecode"`
	PID         int    `json:"pid"`
	Uptime      int64  `json:"uptime"`
	Description string `json:"description"`
	ExitStatus  int    `json:"exitstatus"`
}

// ProcessManager provides process management operations to the API layer.
type ProcessManager interface {
	List() []ProcessInfo
	Get(name string) (ProcessInfo, error)
	Start(name string) error
	Stop(name string) error
	Restart(name string) error
	Signal(name string, sig string) error
	WriteStdin(name string, data []byte) error
	ReadLog(name string, stream string, offset int64, length int) ([]byte, error)

	// Bulk actions over every configured process, in priority order
	// (ascending for start, descending for stop). Each returns one result per
	// process acted on, never nil.
	StartAllOrdered() []ActionResult
	StopAllOrdered() []ActionResult
	RestartAllOrdered() []ActionResult
	// ShuttingDown reports whether process shutdown has begun.
	ShuttingDown() bool
}

// ActionResult statuses.
const (
	ActionOK      = "ok"
	ActionSkipped = "skipped"
	ActionError   = "error"
)

// ActionResult is the outcome of a bulk action for one process. Error is set
// only when Status is ActionError.
type ActionResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// GroupManager provides group-level operations.
type GroupManager interface {
	ListGroups() []string
	StartGroup(name string) error
	StopGroup(name string) error
	RestartGroup(name string) error
}

// ConfigManager provides config management operations.
type ConfigManager interface {
	GetConfig() any
	Reload() (added, changed, removed []string, err error)
	PreviewReload() (added, changed, removed []string, err error)
}

// DaemonInfo describes the running daemon.
type DaemonInfo interface {
	IsShuttingDown() bool
	IsReady() bool
	CheckReady(processes []string) (ready bool, pending []string, err error)
	Version() map[string]string
	PID() int
	Shutdown()
}

// Server is the HTTP API server for Kahi.
type Server struct {
	processes  ProcessManager
	groups     GroupManager
	config     ConfigManager
	daemon     DaemonInfo
	bus        *events.Bus
	logger     *slog.Logger
	mux        *http.ServeMux
	handler    http.Handler // s.mux behind cross-origin protection; what both servers serve
	unixLn     net.Listener
	tcpLn      net.Listener
	unixServer *http.Server
	tcpServer  *http.Server
	ui         http.Handler // Web UI under /ui/; nil when disabled

	// bulkMu allows one bulk action at a time across both listeners.
	bulkMu sync.Mutex

	authUser string
	authPass string // bcrypt hash
}

// Config holds API server configuration.
type Config struct {
	UnixSocket string
	SocketMode os.FileMode
	TCPAddr    string
	TCPEnabled bool
	Username   string
	Password   string // bcrypt hash

	// UI serves the Web UI under /ui/ behind requireAuth; nil disables it.
	UI http.Handler
}

// NewServer creates an API server with the given dependencies.
func NewServer(cfg Config, pm ProcessManager, gm GroupManager, cm ConfigManager, di DaemonInfo, bus *events.Bus, logger *slog.Logger) *Server {
	s := &Server{
		processes: pm,
		groups:    gm,
		config:    cm,
		daemon:    di,
		bus:       bus,
		logger:    logger,
		ui:        cfg.UI,
		authUser:  cfg.Username,
		authPass:  cfg.Password,
	}
	s.mux = s.buildMux()
	s.handler = protectCrossOrigin(s.mux)
	return s
}

// protectCrossOrigin wraps the route table so a browser cannot be made to send
// state-changing requests on behalf of another site.
//
// SEC-025 / ADR-028: the API authenticates with HTTP Basic credentials, which a
// browser caches per origin and attaches to cross-site requests. Process and
// group actions, config reload and shutdown read no body, and signal and stdin
// decode JSON without a Content-Type check, so without this a cross-site HTML
// form could drive any of them, up to shutting the daemon down. The standard
// library check rejects unsafe-method requests whose Sec-Fetch-Site is anything
// but same-origin or none, or, lacking that header, whose Origin host[:port]
// differs from Host. Requests with neither header (kahi ctl, curl, scripts) are
// allowed. GET, HEAD and OPTIONS always pass, so no safe-method handler may
// change state.
//
// It wraps the whole mux, so it runs before requireAuth: a cross-site request
// is refused the same way whatever its credentials.
func protectCrossOrigin(next http.Handler) http.Handler {
	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected", errorCode(http.StatusForbidden))
	}))
	return csrf.Handler(next)
}

func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Probe endpoints -- no auth required.
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	// API v1 endpoints -- auth required on TCP.
	mux.HandleFunc("GET /api/v1/processes", s.requireAuth(s.handleListProcesses))
	mux.HandleFunc("GET /api/v1/processes/{name}", s.requireAuth(s.handleGetProcess))
	mux.HandleFunc("POST /api/v1/processes/{name}/start", s.requireAuth(s.handleStartProcess))
	mux.HandleFunc("POST /api/v1/processes/{name}/stop", s.requireAuth(s.handleStopProcess))
	mux.HandleFunc("POST /api/v1/processes/{name}/restart", s.requireAuth(s.handleRestartProcess))
	mux.HandleFunc("POST /api/v1/processes/{name}/signal", s.requireAuth(s.handleSignalProcess))
	mux.HandleFunc("POST /api/v1/processes/{name}/stdin", s.requireAuth(s.handleWriteStdin))
	mux.HandleFunc("GET /api/v1/processes/{name}/log/{stream}", s.requireAuth(s.handleReadLog))
	mux.HandleFunc("GET /api/v1/processes/{name}/log/{stream}/stream", s.requireAuth(s.handleStreamLog))

	mux.HandleFunc("GET /api/v1/groups", s.requireAuth(s.handleListGroups))
	mux.HandleFunc("POST /api/v1/groups/{name}/start", s.requireAuth(s.handleStartGroup))
	mux.HandleFunc("POST /api/v1/groups/{name}/stop", s.requireAuth(s.handleStopGroup))
	mux.HandleFunc("POST /api/v1/groups/{name}/restart", s.requireAuth(s.handleRestartGroup))

	mux.HandleFunc("GET /api/v1/config", s.requireAuth(s.handleGetConfig))
	mux.HandleFunc("POST /api/v1/config/reload", s.requireAuth(s.handleReloadConfig))

	mux.HandleFunc("POST /api/v1/shutdown", s.requireAuth(s.handleShutdown))
	mux.HandleFunc("GET /api/v1/version", s.requireAuth(s.handleVersion))

	mux.HandleFunc("GET /api/v1/events/stream", s.requireAuth(s.handleEventStream))

	// Bulk actions (FUNC-083). POST only, so no safe-method route changes state.
	mux.HandleFunc("POST /api/v1/all/start", s.requireAuth(s.handleBulk(func() []ActionResult { return s.processes.StartAllOrdered() })))
	mux.HandleFunc("POST /api/v1/all/stop", s.requireAuth(s.handleBulk(func() []ActionResult { return s.processes.StopAllOrdered() })))
	mux.HandleFunc("POST /api/v1/all/restart", s.requireAuth(s.handleBulk(func() []ActionResult { return s.processes.RestartAllOrdered() })))

	// Web UI (FUNC-084, ADR-029): one authenticated subtree, so a UI route
	// cannot be registered without authentication.
	if s.ui != nil {
		mux.HandleFunc("GET /ui/", s.requireAuth(s.ui.ServeHTTP))
		mux.HandleFunc("GET /{$}", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ui/", http.StatusFound)
		}))
	}

	return mux
}

// Start begins serving on the configured listeners.
func (s *Server) Start() error {
	return s.StartWithContext(context.Background())
}

// StartWithContext begins serving on the configured listeners with a context.
func (s *Server) StartWithContext(ctx context.Context) error {
	// This is a no-op; listeners are started via StartUnix/StartTCP.
	_ = ctx
	return nil
}

// StartUnix creates and begins serving on a Unix domain socket.
func (s *Server) StartUnix(path string, mode os.FileMode) error {
	// Remove stale socket from previous run.
	if err := removeStaleSocket(path); err != nil {
		return fmt.Errorf("cannot create socket: %s: %w", path, err)
	}

	// SEC-023: bind under a tightened umask so the socket is owner-only from
	// the moment it exists, not only once the chmod below has run. Unix-socket
	// peers skip authentication, so a connection accepted before the chmod
	// would be a full control session.
	ln, err := sockutil.ListenUnix(path)
	if err != nil {
		return fmt.Errorf("cannot create socket: %s: %w", path, err)
	}

	if err := chmodSocket(path, mode); err != nil {
		_ = ln.Close()
		return fmt.Errorf("cannot set socket permissions: %s: %w", path, err)
	}

	s.unixLn = ln
	s.unixServer = s.newHTTPServer()

	go func() {
		if err := s.unixServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("unix server error", "error", err)
		}
	}()

	s.logger.Info("unix socket server started", "path", path)
	return nil
}

// readHeaderTimeout bounds how long a client may take to send request
// headers, which are read before authentication. It is a var so tests can
// shorten it.
var readHeaderTimeout = 10 * time.Second

// newHTTPServer returns the server used for both listeners. Only the header
// read is bounded: ReadTimeout and WriteTimeout would cut off the
// long-lived SSE streams and attach sessions.
func (s *Server) newHTTPServer() *http.Server {
	return &http.Server{Handler: s.handler, ReadHeaderTimeout: readHeaderTimeout}
}

// StartTCP begins serving on a TCP address.
func (s *Server) StartTCP(addr string) error {
	// Fail-closed TCP authentication (SEC-015): refuse to open the listener
	// without credentials for any bind address, loopback included. This is a
	// defense-in-depth guard mirroring the config-validation gate; the Unix
	// socket remains the password-free local path.
	if s.authUser == "" || s.authPass == "" {
		return fmt.Errorf("http listener on %s requires username/password", addr)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot bind %s: %w", addr, err)
	}

	s.tcpLn = ln
	s.tcpServer = s.newHTTPServer()

	// Warn about binding to all interfaces.
	host, _, _ := net.SplitHostPort(addr)
	if host == "0.0.0.0" || host == "" || host == "::" {
		s.logger.Warn("HTTP server bound to all interfaces", "addr", addr)
	}

	go func() {
		if err := s.tcpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("tcp server error", "error", err)
		}
	}()

	s.logger.Info("tcp http server started", "addr", addr)
	return nil
}

// Stop gracefully shuts down all listeners. Shutdown waits for active
// handlers, and an SSE stream only ends when its client goes away, so once ctx
// expires the remaining connections are closed.
func (s *Server) Stop(ctx context.Context) error {
	var errs []error
	for _, srv := range []*http.Server{s.unixServer, s.tcpServer} {
		if srv == nil {
			continue
		}
		if err := srv.Shutdown(ctx); err != nil {
			errs = append(errs, err)
			if ctx.Err() != nil {
				if cerr := srv.Close(); cerr != nil {
					errs = append(errs, cerr)
				}
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("server shutdown errors: %v", errs)
	}
	return nil
}

// UnixAddr returns the address of the Unix listener, or empty if not started.
func (s *Server) UnixAddr() string {
	if s.unixLn != nil {
		return s.unixLn.Addr().String()
	}
	return ""
}

// TCPAddr returns the address of the TCP listener, or empty if not started.
func (s *Server) TCPAddr() string {
	if s.tcpLn != nil {
		return s.tcpLn.Addr().String()
	}
	return ""
}

func removeStaleSocket(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket", path)
	}
	return os.Remove(path)
}

// --- HTTP Handlers ---

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.daemon != nil && s.daemon.IsShuttingDown() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "shutting_down",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	processFilter := r.URL.Query().Get("process")
	if processFilter != "" {
		processes := strings.Split(processFilter, ",")
		ready, pending, err := s.daemon.CheckReady(processes)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "BAD_REQUEST")
			return
		}
		if !ready {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":  "not_ready",
				"pending": pending,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}

	if s.daemon != nil && s.daemon.IsReady() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"status": "not_ready",
	})
}

func (s *Server) handleListProcesses(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.processes.List())
}

func (s *Server) handleGetProcess(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	info, err := s.processes.Get(name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error(), "NOT_FOUND")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleStartProcess(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.daemon != nil && s.daemon.IsShuttingDown() {
		writeError(w, http.StatusConflict, "daemon is shutting down", "SHUTTING_DOWN")
		return
	}
	if err := s.processes.Start(name); err != nil {
		statusCode := classifyError(err)
		writeError(w, statusCode, err.Error(), errorCode(statusCode))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started", "name": name})
}

func (s *Server) handleStopProcess(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.processes.Stop(name); err != nil {
		statusCode := classifyError(err)
		writeError(w, statusCode, err.Error(), errorCode(statusCode))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped", "name": name})
}

func (s *Server) handleRestartProcess(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.processes.Restart(name); err != nil {
		statusCode := classifyError(err)
		writeError(w, statusCode, err.Error(), errorCode(statusCode))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted", "name": name})
}

func (s *Server) handleSignalProcess(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Signal string `json:"signal"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Signal == "" {
		writeError(w, http.StatusBadRequest, "request body must contain {\"signal\":\"NAME\"}", "BAD_REQUEST")
		return
	}
	if err := s.processes.Signal(name, body.Signal); err != nil {
		statusCode := classifyError(err)
		writeError(w, statusCode, err.Error(), errorCode(statusCode))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signaled", "name": name, "signal": body.Signal})
}

func (s *Server) handleWriteStdin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Data string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "request body must contain {\"data\":\"...\"}", "BAD_REQUEST")
		return
	}
	if err := s.processes.WriteStdin(name, []byte(body.Data)); err != nil {
		statusCode := classifyError(err)
		writeError(w, statusCode, err.Error(), errorCode(statusCode))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "written", "name": name})
}

func (s *Server) handleReadLog(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	stream := r.PathValue("stream")
	if stream != "stdout" && stream != "stderr" {
		writeError(w, http.StatusBadRequest, "stream must be stdout or stderr", "BAD_REQUEST")
		return
	}

	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	const maxReadLength = 65536 // 64KB, matches ring buffer capacity
	length := 1600
	if l := r.URL.Query().Get("length"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			if v > maxReadLength {
				writeError(w, http.StatusBadRequest, "length exceeds maximum of 65536", "BAD_REQUEST")
				return
			}
			length = v
		}
	}

	data, err := s.processes.ReadLog(name, stream, offset, length)
	if err != nil {
		statusCode := classifyError(err)
		writeError(w, statusCode, err.Error(), errorCode(statusCode))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}

// handleBulk runs one bulk action (FUNC-083). The shutdown check comes before
// the lock, so SHUTTING_DOWN wins over CONFLICT. The supervisor's flag covers
// API-triggered shutdown; the manager's covers signal-driven shutdown, where
// the supervisor's flag is never set. The action ignores the request context,
// so a client disconnect does not abort it.
func (s *Server) handleBulk(action func() []ActionResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if (s.daemon != nil && s.daemon.IsShuttingDown()) || s.processes.ShuttingDown() {
			writeError(w, http.StatusConflict, "daemon is shutting down", "SHUTTING_DOWN")
			return
		}
		if !s.bulkMu.TryLock() {
			writeError(w, http.StatusConflict, "bulk action in progress", errorCode(http.StatusConflict))
			return
		}
		defer s.bulkMu.Unlock()

		results := action()
		if results == nil {
			results = []ActionResult{}
		}
		writeJSON(w, http.StatusOK, map[string][]ActionResult{"results": results})
	}
}

func (s *Server) handleStreamLog(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	stream := r.PathValue("stream")
	if stream != "stdout" && stream != "stderr" {
		writeError(w, http.StatusBadRequest, "stream must be stdout or stderr", "BAD_REQUEST")
		return
	}

	// Verify process exists.
	if _, err := s.processes.Get(name); err != nil {
		writeError(w, http.StatusNotFound, err.Error(), "NOT_FOUND")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported", "SERVER_ERROR")
		return
	}

	// Determine the event type based on stream.
	var eventType events.EventType
	if stream == "stdout" {
		eventType = events.ProcessLogStdout
	} else {
		eventType = events.ProcessLogStderr
	}

	// Subscribe before flushing the headers: a client that reads its backlog
	// once the stream is open (the Web UI log viewer) then cannot miss a chunk
	// published in between. A channel serializes writes to the response.
	ch := make(chan string, 64)
	id := s.bus.Subscribe(eventType, func(e events.Event) {
		if e.Data["name"] != name {
			return
		}
		select {
		case ch <- e.Data["data"]:
		default:
		}
	})
	defer s.bus.Unsubscribe(id)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			writeSSEData(w, data)
			flusher.Flush()
		}
	}
}

// writeSSEData writes data as one SSE "data:" field per line, ended by a single
// blank line. Emitting each line separately means embedded newlines or
// "event:"/"data:" prefixes in (untrusted) process output are carried as literal
// data and cannot inject additional SSE frames. See SEC-013. SSE also ends a
// line at CR or CRLF, so both are normalized to LF first.
var sseLineEnds = strings.NewReplacer("\r\n", "\n", "\r", "\n")

func writeSSEData(w io.Writer, data string) {
	data = sseLineEnds.Replace(data)
	for line := range strings.SplitSeq(data, "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = fmt.Fprint(w, "\n")
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.groups.ListGroups())
}

func (s *Server) handleStartGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.groups.StartGroup(name); err != nil {
		statusCode := classifyError(err)
		writeError(w, statusCode, err.Error(), errorCode(statusCode))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started", "group": name})
}

func (s *Server) handleStopGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.groups.StopGroup(name); err != nil {
		statusCode := classifyError(err)
		writeError(w, statusCode, err.Error(), errorCode(statusCode))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped", "group": name})
}

func (s *Server) handleRestartGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.groups.RestartGroup(name); err != nil {
		statusCode := classifyError(err)
		writeError(w, statusCode, err.Error(), errorCode(statusCode))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted", "group": name})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sanitizeConfigView(s.config.GetConfig()))
}

// sanitizeConfigView redacts secrets from any config-returning response. When
// the value is a *config.Config it is replaced by the sanitized view (masked
// environment and webhook header values, stripped webhook URL userinfo, and
// json-excluded credentials); other values pass through unchanged.
func sanitizeConfigView(v any) any {
	if c, ok := v.(*config.Config); ok {
		return config.Sanitized(c)
	}
	return v
}

func (s *Server) handleReloadConfig(w http.ResponseWriter, r *http.Request) {
	// dry_run=true reports the diff without applying it (ctl reread).
	preview := r.URL.Query().Get("dry_run") == "true"
	reload, status := s.config.Reload, "reloaded"
	if preview {
		reload, status = s.config.PreviewReload, "preview"
	}
	added, changed, removed, err := reload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "SERVER_ERROR")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  status,
		"added":   added,
		"changed": changed,
		"removed": removed,
	})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "shutting_down"})
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.daemon.Shutdown()
	}()
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.daemon.Version())
}

func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported", "SERVER_ERROR")
		return
	}

	// Parse type filter.
	typesParam := r.URL.Query().Get("types")
	var typeFilter map[events.EventType]bool
	if typesParam != "" {
		typeFilter = make(map[events.EventType]bool)
		for t := range strings.SplitSeq(typesParam, ",") {
			typeFilter[events.EventType(strings.TrimSpace(t))] = true
		}
	}

	// Subscribe to all process state events + supervisor events.
	allTypes := []events.EventType{
		events.ProcessStateStopped, events.ProcessStateStarting,
		events.ProcessStateRunning, events.ProcessStateBackoff,
		events.ProcessStateStopping, events.ProcessStateExited,
		events.ProcessStateFatal,
		events.SupervisorStateRunning, events.SupervisorStateStopping,
		events.ProcessGroupAdded, events.ProcessGroupRemoved,
	}

	// Use a channel to serialize writes to the response writer.
	type sseEvent struct {
		eventType string
		data      []byte
	}
	ch := make(chan sseEvent, 64)

	var ids []uint64
	for _, et := range allTypes {
		if typeFilter != nil && !typeFilter[et] {
			continue
		}
		id := s.bus.Subscribe(et, func(e events.Event) {
			data, _ := json.Marshal(e.Data)
			select {
			case ch <- sseEvent{eventType: string(e.Type), data: data}:
			default:
			}
		})
		ids = append(ids, id)
	}

	defer func() {
		for _, id := range ids {
			s.bus.Unsubscribe(id)
		}
	}()

	// Headers go out only after subscribing, so an event published once the
	// client sees the stream open is delivered (FUNC-078 refreshes on open).
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.eventType, ev.data)
			flusher.Flush()
		}
	}
}

// --- Auth middleware ---

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Unix socket connections skip auth.
		if isUnixConn(r) {
			next(w, r)
			return
		}

		// TCP connections require auth if configured.
		if s.authUser == "" {
			next(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="kahi"`)
			writeError(w, http.StatusUnauthorized, "authentication required", "UNAUTHORIZED")
			return
		}

		// Evaluate both comparisons unconditionally so timing does not
		// distinguish a wrong username from a wrong password.
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.authUser)) == 1
		passOK := checkPassword(pass, s.authPass)
		if !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="kahi"`)
			writeError(w, http.StatusUnauthorized, "invalid credentials", "UNAUTHORIZED")
			return
		}

		next(w, r)
	}
}

func isUnixConn(r *http.Request) bool {
	// When served over Unix socket, RemoteAddr is typically empty or "@".
	return r.RemoteAddr == "" || r.RemoteAddr == "@"
}

// checkPassword reports whether plain matches the configured bcrypt hash.
// Config validation guarantees hash is a bcrypt hash ($2 prefix); a non-bcrypt
// hash reaching here fails closed via bcrypt.CompareHashAndPassword. bcrypt's
// compare is constant-time with respect to the password.
func checkPassword(plain, hash string) bool {
	if hash == "" {
		return plain == ""
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message, code string) {
	writeJSON(w, status, map[string]string{
		"error": message,
		"code":  code,
	})
}

func classifyError(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such process"),
		strings.Contains(msg, "no such group"):
		return http.StatusNotFound
	case strings.Contains(msg, "already started"),
		strings.Contains(msg, "already running"):
		return http.StatusConflict
	case strings.Contains(msg, "invalid signal"),
		strings.Contains(msg, "not running"),
		strings.Contains(msg, "does not accept stdin"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func errorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "BAD_REQUEST"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "CONFLICT"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	default:
		return "SERVER_ERROR"
	}
}
