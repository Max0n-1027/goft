// Package logging builds the JSON Lines logger used throughout goft.
package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

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
	EventVerify     = "verify"
	EventPostAction = "postaction"
	EventConnect    = "connect"
	EventLifecycle  = "lifecycle"
	EventSummary    = "summary"
)

// Options configures the logger for one process.
type Options struct {
	// Log is the configured destination, rotation and field selection.
	Log config.Log
	// Level is the threshold, already resolved from Log.Level and any override.
	Level slog.Level
	// Job and Direction are attached to every record, so that the output of
	// several processes can be collected in one place and still be told apart.
	Job       string
	Direction config.Direction

	// ConsoleEnabled moves the JSON log to stderr when no log file is
	// configured, leaving stdout to the human readable output.
	ConsoleEnabled bool

	// Now is used for date based rotation; nil means time.Now.
	Now func() time.Time
}

// nopCloser lets New always return something closable.
type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// New builds the process logger and the closer for its backing file, if any.
func New(opts Options) (*slog.Logger, io.Closer, error) {
	var (
		w      io.Writer
		closer io.Closer = nopCloser{}
	)

	switch {
	case opts.Log.Path != "":
		if dir := filepath.Dir(opts.Log.Path); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, nil, err
			}
		}
		now := opts.Now
		if now == nil {
			now = time.Now
		}
		rw := newRotatingWriter(opts.Log, now)
		w, closer = rw, rw
	case opts.ConsoleEnabled:
		// stdout belongs to the console output in this case.
		w = os.Stderr
	default:
		w = os.Stdout
	}

	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: opts.Level})
	l := slog.New(h).With(KeyJob, opts.Job, KeyDirection, string(opts.Direction))
	return l, closer, nil
}
