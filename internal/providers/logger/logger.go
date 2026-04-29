// Package logger is a thin slog wrapper. The daemon's service / runtime
// layers consume `*slog.Logger` directly; this package exists to centralize
// construction (level / format / destination).
package logger

import (
	"fmt"
	"io"
	"log/slog"
)

// Build constructs an *slog.Logger from level + format strings.
//
// Supported levels: debug, info, warn, error
// Supported formats: text, json
func Build(level, format string, w io.Writer) (*slog.Logger, error) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("invalid log level %q", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("invalid log format %q", format)
	}
}

// Discard returns a logger that silently drops every record.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}
