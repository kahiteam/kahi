// Package logging provides structured logging for Kahi using stdlib slog.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
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
