// Package logging provides structured logging for Kahi using stdlib slog.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// LogConfig controls logger creation.
type LogConfig struct {
	Level  string    // "debug", "info", "warn", "error"
	Format string    // "json" (default), "text"
	Output io.Writer // defaults to os.Stdout
}

// New creates a configured *slog.Logger.
func New(cfg LogConfig) *slog.Logger {
	out := cfg.Output
	if out == nil {
		out = os.Stdout
	}

	opts := &slog.HandlerOptions{
		Level: parseLevel(cfg.Level),
	}

	var handler slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		handler = slog.NewTextHandler(out, opts)
	} else {
		handler = slog.NewJSONHandler(out, opts)
	}

	return slog.New(handler)
}

// WithFields returns a child logger with additional context fields.
func WithFields(logger *slog.Logger, fields ...any) *slog.Logger {
	return logger.With(fields...)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ValidateLevel returns an error if the level string is not recognized.
func ValidateLevel(s string) error {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("invalid log level: %q (must be debug, info, warn, or error)", s)
	}
}

// DaemonLog is the daemon's logger and, when it writes to a file, that file,
// which SIGUSR2 reopens after logrotate renames it.
type DaemonLog struct {
	Logger *slog.Logger
	file   *ReopenableFile
}

// OpenDaemonLog creates the daemon logger, writing to logfile or, when it is
// empty, to stdout.
func OpenDaemonLog(level, format, logfile string) (*DaemonLog, error) {
	dl := &DaemonLog{}
	var out io.Writer = os.Stdout
	if logfile != "" {
		f, err := OpenReopenableFile(logfile)
		if err != nil {
			return nil, fmt.Errorf("cannot open log file: %s: %w", logfile, err)
		}
		dl.file = f
		out = f
	}
	dl.Logger = New(LogConfig{Level: level, Format: format, Output: out})
	return dl, nil
}

// Reopen reopens the log file at its path; a no-op when logging to stdout.
func (d *DaemonLog) Reopen() error {
	if d.file == nil {
		return nil
	}
	return d.file.Reopen()
}

// Close closes the log file; a no-op when logging to stdout.
func (d *DaemonLog) Close() error {
	if d.file == nil {
		return nil
	}
	return d.file.Close()
}

// ReopenableFile is a log file writer whose file can be reopened at the same
// path while other goroutines write to it.
type ReopenableFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

// OpenReopenableFile opens path for appending, with the same symlink
// protections as the capture logs.
func OpenReopenableFile(path string) (*ReopenableFile, error) {
	f, err := openLogFile(path, 0644)
	if err != nil {
		return nil, err
	}
	return &ReopenableFile{path: path, f: f}, nil
}

func (r *ReopenableFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Write(p)
}

// Reopen opens the path again and swaps the new file in. On failure the old
// file stays in use, so no output is lost.
func (r *ReopenableFile) Reopen() error {
	f, err := openLogFile(r.path, 0644)
	if err != nil {
		return fmt.Errorf("cannot reopen log file: %s: %w", r.path, err)
	}
	r.mu.Lock()
	old := r.f
	r.f = f
	r.mu.Unlock()
	return old.Close()
}

// Close closes the current file.
func (r *ReopenableFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

// LevelVar wraps a slog.LevelVar for dynamic level changes at runtime.
type LevelVar struct {
	level atomic.Value
}

// NewLevelVar creates a LevelVar initialized to the given level.
func NewLevelVar(level string) *LevelVar {
	lv := &LevelVar{}
	lv.level.Store(parseLevel(level))
	return lv
}

// Set changes the log level at runtime.
func (lv *LevelVar) Set(level string) {
	lv.level.Store(parseLevel(level))
}

// Level returns the current slog.Level.
func (lv *LevelVar) Level() slog.Level {
	return lv.level.Load().(slog.Level)
}
