package config

import (
	"log/slog"
	"strings"

	"goft/internal/i18n"
)

// ParseLevel converts a configured level name into a slog.Level.
//
// The level is the only verbosity knob in goft: it controls both the JSON log
// and the human readable console output.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, i18n.Errorf("%q is invalid (debug | info | warn | error)", name)
	}
}
