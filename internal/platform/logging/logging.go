// Package logging builds the process-wide structured logger. Components
// receive a *slog.Logger by injection; nothing logs through a package global.
package logging

import (
	"io"
	"log/slog"
)

// New returns a slog.Logger writing to w in the given format ("json" or
// "text") at the given level ("debug", "info", "warn", "error"). Invalid
// values fall back to text/info; validation belongs to config.Load.
func New(w io.Writer, format, level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h)
}
