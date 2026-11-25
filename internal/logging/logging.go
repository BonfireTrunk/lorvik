// Package logging builds the process-wide slog logger from configuration.
package logging

import (
	"io"
	"log/slog"
)

// New returns a logger writing to w at the given level in the given format.
// Unknown values fall back to info and text so logging never fails startup;
// config validation reports bad values before this is called.
func New(w io.Writer, level, format string) *slog.Logger {
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
