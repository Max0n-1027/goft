package config

import (
	"fmt"
	"log/slog"
	"strings"
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
		return 0, fmt.Errorf("%q is invalid (debug | info | warn | error)", name)
	}
}
