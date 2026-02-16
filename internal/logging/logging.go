// Package logging provides structured logging for Kahi using stdlib slog.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
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

// DaemonLogger creates a logger for the daemon, optionally writing to a file.
// Returns the logger and a cleanup function to close the file (if opened).
func DaemonLogger(level, format, logfile string) (*slog.Logger, func(), error) {
	var out io.Writer = os.Stdout
	var cleanup func()

	if logfile != "" {
		f, err := os.OpenFile(logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot open log file: %s: %w", logfile, err)
		}
		out = f
		cleanup = func() { f.Close() }
	}

	logger := New(LogConfig{
		Level:  level,
		Format: format,
		Output: out,
	})

	return logger, cleanup, nil
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
