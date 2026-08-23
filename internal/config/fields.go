package config

import (
	"log/slog"
	"strings"
)

// Names of the parameters a per-file transfer record can carry. They are the
// vocabulary of log.fields, which is why they live with the configuration
// rather than with the logger.
const (
	FieldSrc        = "src"
	FieldDst        = "dst"
	FieldProtocol   = "protocol"
	FieldBytes      = "bytes"
	FieldDurationMS = "duration_ms"
	FieldVerify     = "verify"
	FieldResult     = "result"
	FieldReason     = "reason"
	FieldError      = "error"
	FieldHashSrc    = "hash_src"
	FieldHashDst    = "hash_dst"
	FieldAttempt    = "attempt"
	FieldRateMiBs   = "rate_mibs"
)

// SelectableLogFields is everything log.fields accepts, in the order it is
// reported back to the user.
var SelectableLogFields = []string{
	FieldSrc, FieldDst, FieldProtocol, FieldBytes, FieldDurationMS,
	FieldVerify, FieldResult, FieldReason, FieldError,
	FieldHashSrc, FieldHashDst, FieldAttempt, FieldRateMiBs,
}

// DefaultLogFields applies when log.fields is not set.
//
// Everything but the transfer rate, which is a performance question rather than
// a record of what happened.
var DefaultLogFields = []string{
	FieldSrc, FieldDst, FieldProtocol, FieldBytes, FieldDurationMS,
	FieldVerify, FieldResult, FieldReason, FieldError,
	FieldHashSrc, FieldHashDst, FieldAttempt,
}

// LogFieldSet answers whether a field should be written.
type LogFieldSet map[string]bool

// Has reports whether the field is wanted.
func (s LogFieldSet) Has(field string) bool { return s[field] }

// LogFields resolves log.fields into a set.
//
// Record identity — time, level, msg, job, direction and event — is never
// selectable: a line missing those cannot be interpreted at all.
//
// When the configuration names no fields the default set applies, and at debug
// level it also picks up the transfer rate, which is what debug meant before
// the set became configurable. Naming fields explicitly replaces all of that:
// the list is then exactly what gets written.
func (c *Config) LogFields(level slog.Level) LogFieldSet {
	if c.Log.Fields != nil {
		set := make(LogFieldSet, len(c.Log.Fields))
		for _, n := range c.Log.Fields {
			set[strings.ToLower(strings.TrimSpace(n))] = true
		}
		return set
	}

	set := make(LogFieldSet, len(DefaultLogFields)+1)
	for _, n := range DefaultLogFields {
		set[n] = true
	}
	if level <= slog.LevelDebug {
		set[FieldRateMiBs] = true
	}
	return set
}
