// Package web serves the Kahi web dashboard with embedded static assets.
package web

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// Handler serves the Kahi web UI.
type Handler struct {
	lister    ProcessLister
	templates *template.Template
	staticFS  http.FileSystem
	logger    *slog.Logger
}

// Config configures the web handler.
type Config struct {
	StaticDir string // override embedded assets with files from this directory
}

// NewHandler creates a web UI handler.
func NewHandler(lister ProcessLister, cfg Config, logger *slog.Logger) (*Handler, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("cannot parse templates: %w", err)
	}

	var sfs http.FileSystem
	if cfg.StaticDir != "" {
		if info, err := os.Stat(cfg.StaticDir); err != nil || !info.IsDir() {
			logger.Warn("static_dir not found, using embedded assets", "path", cfg.StaticDir)
			sub, _ := fs.Sub(staticFS, "static")
			sfs = http.FS(sub)
		} else {
			sfs = http.Dir(cfg.StaticDir)
		}
	} else {
		sub, _ := fs.Sub(staticFS, "static")
		sfs = http.FS(sub)
	}

	return &Handler{
		lister:    lister,
		templates: tmpl,
		staticFS:  sfs,
		logger:    logger,
	}, nil
}

// RegisterRoutes adds web UI routes to the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.handleIndex)
	mux.HandleFunc("GET /log/{name}/{stream}", h.handleLog)
	mux.Handle("GET /static/", http.StripPrefix("/static/", h.staticHandler()))
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	var data StatusPageData
	if h.lister != nil {
		data.Processes = h.lister.ListWeb()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		h.logger.Error("template render error", "error", err)
	}
}

func (h *Handler) handleLog(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	stream := r.PathValue("stream")
	if stream != "stdout" && stream != "stderr" {
		http.NotFound(w, r)
		return
	}

	data := LogPageData{
		Name:   name,
		Stream: stream,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "log.html", data); err != nil {
		h.logger.Error("template render error", "error", err)
	}
}

func (h *Handler) staticHandler() http.Handler {
	fileServer := http.FileServer(h.staticFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set content type and caching headers.
		ext := filepath.Ext(r.URL.Path)
		switch ext {
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case ".js":
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		case ".svg":
			w.Header().Set("Content-Type", "image/svg+xml")
		case ".ico":
			w.Header().Set("Content-Type", "image/x-icon")
		}

		// ETag based on file content.
		f, err := h.staticFS.Open(r.URL.Path)
		if err == nil {
			defer f.Close()
			if info, err := f.Stat(); err == nil && !info.IsDir() {
				etag := fmt.Sprintf(`"%x"`, sha256.Sum256([]byte(info.Name()+info.ModTime().String())))
				w.Header().Set("ETag", etag)
				w.Header().Set("Cache-Control", "public, max-age=3600")
				if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		fileServer.ServeHTTP(w, r)
	})
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
