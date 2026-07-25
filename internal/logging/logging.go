// Package logging builds the application's structured logger.
package logging

import (
	"io"
	"log/slog"

	"github.com/gordon2/manualbox/internal/config"
)

// New returns a logger configured from cfg, writing to w.
//
// Source locations are attached only at debug level: they are what you want
// when chasing a bug and pure noise in a homelab's normal log stream.
func New(cfg config.Log, w io.Writer) *slog.Logger {
	level := Level(cfg.Level)
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: level <= slog.LevelDebug,
	}

	var h slog.Handler
	if cfg.Format == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h)
}

// Level maps a configuration string to a [slog.Level], defaulting to info for
// anything unrecognized. Config validation rejects bad values before this is
// reached; the default exists so a logger is always usable, including in tests.
func Level(s string) slog.Level {
	switch s {
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

// Discard returns a logger that throws everything away, for tests.
func Discard() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
