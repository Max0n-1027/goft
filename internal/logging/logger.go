// Package logging builds the JSON Lines logger used throughout goft.
package logging

import (
	"log/slog"

	"goft/internal/config"
)

// Field names shared by every record, so that call sites do not drift.
const (
	// Record identity. These are written whatever log.fields says, because a
	// line without them cannot be placed.
	KeyJob       = "job"
	KeyDirection = "direction"
	// KeyCycleID ties together everything one pass over the sending side
	// produced, which is what makes a long-running log readable.
	KeyCycleID = "cycle_id"
	KeyEvent   = "event"
	KeyStep    = "step"

	// Per-file parameters, named once in the configuration vocabulary so the
	// two cannot drift apart.
	KeySrc        = config.FieldSrc
	KeyDst        = config.FieldDst
	KeyProtocol   = config.FieldProtocol
	KeyBytes      = config.FieldBytes
	KeyDurationMS = config.FieldDurationMS
	KeyVerify     = config.FieldVerify
	KeyResult     = config.FieldResult
	KeyReason     = config.FieldReason
	KeyError      = config.FieldError
	KeyHashSrc    = config.FieldHashSrc
	KeyHashDst    = config.FieldHashDst
	KeyRateMiBs   = config.FieldRateMiBs
	KeyAttempt    = config.FieldAttempt
)

// Event values for the "event" field.
const (
	EventScan       = "scan"
	EventTransfer   = "transfer"
	EventPostAction = "postaction"
	EventConnect    = "connect"
	EventLifecycle  = "lifecycle"
	EventSummary    = "summary"
)

// EventService marks a record about the service itself rather than a job.
const EventService = "service"

// Options configures one logger: a job's, or the service's.
type Options struct {
	// Log is the configured destination, rotation and field selection.
	Log config.Log
	// Level is the threshold, already resolved from Log.Level and any override.
	Level slog.Level
	// Job and Direction are attached to every record, so that the output of
	// several jobs can be collected in one place and still be told apart. The
	// service's own logger has neither.
	Job       string
	Direction config.Direction

	// ConsoleEnabled moves the JSON log to stderr when no log file is
	// configured, leaving stdout to the human readable output.
	ConsoleEnabled bool

	// Outputs supplies the writer, shared with every other logger writing to
	// the same place.
	Outputs *Outputs
}

// New builds a logger writing where opts.Log says.
func New(opts Options) (*slog.Logger, error) {
	w, err := opts.Outputs.For(opts.Log, opts.ConsoleEnabled)
	if err != nil {
		return nil, err
	}
	l := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: opts.Level}))
	if opts.Job != "" {
		l = l.With(KeyJob, opts.Job, KeyDirection, string(opts.Direction))
	}
	return l, nil
}
