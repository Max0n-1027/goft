package config

import "log/slog"

// Secret is a string that never reveals itself through fmt or [log/slog].
// Use string(s) to obtain the actual value.
type Secret string

const redacted = "REDACTED"

// String masks the value so that accidental formatting cannot leak it.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

// LogValue masks the value for slog.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(s.String())
}

// IsSet reports whether the secret holds a value.
func (s Secret) IsSet() bool { return s != "" }
