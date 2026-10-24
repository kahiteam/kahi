// Package web serves the Kahi web dashboard with embedded static assets.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// ProcessView is the template data for a single process row.
type ProcessView struct {
	Name        string
	Group       string
	State       string
	StateLower  string
	PID         int
	Uptime      int64
	UptimeStr   string
	Description string
	ExitStatus  int
}

// StatusPageData is the template data for the status page.
type StatusPageData struct {
	Processes []ProcessView
}

// LogPageData is the template data for the log viewer page.
type LogPageData struct {
	Name   string
	Stream string
}

// ProcessLister provides process data for the web UI.
type ProcessLister interface {
	ListWeb() []ProcessView
}

// Config configures the web handler.
type Config struct {
	StaticDir string // per-file overlay for the embedded static assets
}

// contentSecurityPolicy allows only same-origin resources and no inline script
// or style, so markup that slips past escaping cannot run (SEC-026). The UI has
// no forms, so form-action (not covered by default-src) is closed too.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// SetSecurityHeaders sets the headers every Web UI response carries. It is
// exported so a handler standing in for the UI (the daemon's 503 fallback)
// sends the same ones.
func SetSecurityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
}

// contentTypes is fixed rather than taken from mime.TypeByExtension, which
// reads the host's MIME database and maps .js differently on some systems.
var contentTypes = map[string]string{
	".js":  "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8",
	".svg": "image/svg+xml",
	".ico": "image/x-icon",
}

// Handler serves the Kahi web UI under /ui/.
type Handler struct {
	lister    ProcessLister
	templates *template.Template
	assets    *assets
	mux       *http.ServeMux
	logger    *slog.Logger
}

// NewHandler creates a web UI handler. It fails only if the embedded
// templates do not parse.
func NewHandler(lister ProcessLister, cfg Config, logger *slog.Logger) (*Handler, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{"pathEscape": url.PathEscape}).
		ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("cannot parse templates: %w", err)
	}
	a, err := newAssets(cfg.StaticDir, logger)
	if err != nil {
		return nil, err
	}

	h := &Handler{lister: lister, templates: tmpl, assets: a, logger: logger}
	h.mux = http.NewServeMux()
	h.mux.HandleFunc("GET /ui/{$}", h.handleIndex)
	h.mux.HandleFunc("GET /ui/log/{name}/{stream}", h.handleLog)
	h.mux.HandleFunc("GET /ui/static/{file...}", h.handleStatic)
	return h, nil
}

// ServeHTTP serves the UI routes; every response carries the security headers.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	SetSecurityHeaders(w.Header())
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) processes() []ProcessView {
	if h.lister == nil {
		return nil
	}
	return h.lister.ListWeb()
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	h.render(w, "index.html", StatusPageData{Processes: h.processes()})
}

func (h *Handler) handleLog(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	stream := r.PathValue("stream")
	if stream != "stdout" && stream != "stderr" {
		http.Error(w, "invalid stream", http.StatusBadRequest)
		return
	}
	known := false
	for _, p := range h.processes() {
		if p.Name == name {
			known = true
			break
		}
	}
	if !known {
		http.Error(w, "No such process: "+name, http.StatusNotFound)
		return
	}
	h.render(w, "log.html", LogPageData{Name: name, Stream: stream})
}

func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := h.templates.ExecuteTemplate(&buf, name, data); err != nil {
		h.logger.Error("template render error", "template", name, "error", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (h *Handler) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	data, etag, err := h.assets.read(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ct, ok := contentTypes[path.Ext(name)]; ok {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("ETag", etag)
	// private: responses to authenticated requests must not sit in shared caches.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// assets reads static files from an optional static_dir overlay, falling back
// per file to the embedded copies. The overlay is opened with os.Root, so
// neither ".." nor a symlink leaving the directory can reach outside it.
type assets struct {
	overlay  fs.FS // nil when no usable static_dir is configured
	embedded fs.FS
	etags    map[string]string // embedded file -> ETag, computed once
	logger   *slog.Logger
}

func newAssets(staticDir string, logger *slog.Logger) (*assets, error) {
	embedded, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	a := &assets{embedded: embedded, etags: map[string]string{}, logger: logger}
	entries, err := fs.ReadDir(embedded, ".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(embedded, e.Name())
		if err != nil {
			return nil, err
		}
		a.etags[e.Name()] = contentETag(data)
	}

	if staticDir != "" {
		if info, err := os.Stat(staticDir); err != nil || !info.IsDir() {
			logger.Warn("static_dir not found, using embedded assets", "path", staticDir)
		} else if root, err := os.OpenRoot(staticDir); err != nil {
			logger.Warn("static_dir not found, using embedded assets", "path", staticDir, "error", err)
		} else {
			a.overlay = root.FS()
		}
	}
	return a, nil
}

// read returns a static file and its ETag. The overlay is consulted only for
// the asset types the UI uses (contentTypes): an overlay .html or
// extension-less file would be served as HTML and could run same-origin
// script. A file missing from the overlay comes from the embedded assets; any
// other overlay error (an escape attempt, a permission problem) is returned as
// is, with no fallback. Invalid names such as "../x" are rejected by both file
// systems, as fs.FS requires.
func (a *assets) read(name string) ([]byte, string, error) {
	if _, allowed := contentTypes[path.Ext(name)]; a.overlay != nil && allowed {
		data, err := fs.ReadFile(a.overlay, name)
		if err == nil {
			return data, contentETag(data), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			a.logger.Debug("static_dir file not served", "file", name, "error", err)
			return nil, "", err
		}
	}
	data, err := fs.ReadFile(a.embedded, name)
	if err != nil {
		return nil, "", err
	}
	return data, a.etags[name], nil
}

// contentETag derives a strong ETag from file content, so a changed asset gets
// a new tag even though embedded files carry no modification time.
func contentETag(data []byte) string {
	return fmt.Sprintf(`"%x"`, sha256.Sum256(data))
}

// FormatUptime formats seconds into a human-readable duration.
func FormatUptime(seconds int64) string {
	d := time.Duration(seconds) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
