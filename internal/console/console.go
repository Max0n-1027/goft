// Package console prints a human readable account of a run to stdout, separate
// from the JSON log.
package console

import (
	"fmt"
	"io"
	"log/slog"
	"sync"

	"goft/internal/config"
	"goft/internal/engine"
	"goft/internal/verify"
)

// Console renders transfer progress for a person rather than for a log parser.
//
// Its verbosity follows log.level: there is no separate console setting, so
// raising --log-level makes both outputs more detailed at once.
type Console struct {
	mu    sync.Mutex
	w     io.Writer
	level slog.Level
	color bool

	job       string
	direction config.Direction
	local     string
	remote    string

	// A watching run reports a cycle only once it is over, so that polls where
	// nothing actually moved stay silent. A one-shot run prints as it goes,
	// because someone is waiting to watch it happen.
	buffered bool
	pending  []string
	notable  bool
}

// Options configures a console.
type Options struct {
	// Writer is where the output goes, normally stdout.
	Writer io.Writer
	// Level follows log.level: it decides which results are worth a line.
	Level slog.Level
	// Color turns on ANSI colouring, and should only be set for a terminal.
	Color bool
	// Job names the job in the header.
	Job string
	// Direction picks the arrow drawn between the two locations.
	Direction config.Direction
	// Local and Remote are the two ends, printed either side of the arrow.
	Local  string
	Remote string

	// Buffered holds a cycle's output back until the cycle ends, and drops it
	// if nothing was transferred. Set it for serve, not for one-shot runs.
	Buffered bool
}

// New builds a console.
func New(o Options) *Console {
	return &Console{
		w:         o.Writer,
		level:     o.Level,
		color:     o.Color,
		job:       o.Job,
		direction: o.Direction,
		local:     o.Local,
		remote:    o.Remote,
		buffered:  o.Buffered,
	}
}

// emit prints a line, or holds it back until the cycle is judged worth showing.
func (c *Console) emit(line string) {
	if c.buffered {
		c.pending = append(c.pending, line)
		return
	}
	fmt.Fprintln(c.w, line)
}

// Plan prints the header for a cycle, and for a dry run the list of files that
// would be transferred.
func (c *Console) Plan(p engine.Plan) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.pending, c.notable = nil, false

	// A polling cycle that found nothing stays silent.
	if len(p.Files) == 0 && !p.DryRun {
		c.notable = false
		return
	}

	route := fmt.Sprintf("%s %s %s", c.local, c.direction.Arrow(), c.remote)
	if p.DryRun {
		c.notable = true
		c.emit(fmt.Sprintf("goft %s  %s  (dry-run)", c.job, route))
		for _, f := range p.Files {
			c.emit(fmt.Sprintf("  %-40s %10s", f.Path, humanBytes(f.Size)))
		}
		return
	}
	c.emit(fmt.Sprintf("goft %s  %s  (%d files, %s)", c.job, route, len(p.Files), humanBytes(p.Bytes)))
}

// Result prints one file's outcome, if the current level admits it.
func (c *Console) Result(r engine.Result) {
	if r.Level() < c.level {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	width := len(fmt.Sprint(r.Total))
	status := c.paint(r)

	line := fmt.Sprintf("[%*d/%d] %-40s %10s  %s %6.1fs",
		width, r.Index, r.Total, r.Path, humanBytes(r.Bytes), status, r.Elapsed.Seconds())

	switch {
	case r.Err != nil:
		line += "  " + r.Err.Error()
	case r.Reason != "":
		line += "  " + string(r.Reason)
	}
	if r.Attempts > 1 {
		line += fmt.Sprintf("  (attempt %d)", r.Attempts)
	}
	if c.level <= slog.LevelDebug && r.Hashed {
		line += fmt.Sprintf("  %s %.1f MiB/s", verify.Format(r.DstHash), rate(r))
	}

	// A cycle is worth showing if something moved, or if something needs
	// attention. Repeatedly skipping the same files is neither.
	if r.Outcome != engine.Skipped || r.Level() >= slog.LevelWarn {
		c.notable = true
	}
	c.emit(line)
}

// CycleError reports a cycle that never got going, such as a remote that
// cannot be reached. Without this a watching run with the console on would show
// nothing at all while failing every poll.
func (c *Console) CycleError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.pending, c.notable = nil, false
	fmt.Fprintf(c.w, "goft %s  %s\n", c.job, err.Error())
}

// Summary closes out a cycle.
func (c *Console) Summary(s engine.Summary) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch {
	case s.PlannedOnly:
		c.emit(fmt.Sprintf("%d files, %s would be transferred (nothing was sent)",
			s.Total, humanBytes(s.Bytes)))
	case s.Total > 0:
		c.emit(fmt.Sprintf("%d files: %d transferred (%s), %d skipped, %d failed  in %.1fs",
			s.Total, s.Succeeded, humanBytes(s.Bytes), s.Skipped, s.Failed, s.Elapsed.Seconds()))
	}

	if c.buffered {
		if c.notable {
			for _, line := range c.pending {
				fmt.Fprintln(c.w, line)
			}
		}
		c.pending, c.notable = nil, false
	}
}

// ANSI colours, used only when stdout is a terminal.
const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
)

func (c *Console) paint(r engine.Result) string {
	label, color := "ok", ansiGreen
	switch r.Outcome {
	case engine.Skipped:
		label, color = "skipped", ansiYellow
	case engine.Failed:
		label, color = "failed", ansiRed
	}
	// Pad before colouring: escape sequences would otherwise be counted as
	// characters and throw the column alignment off.
	padded := fmt.Sprintf("%-8s", label)
	if !c.color {
		return padded
	}
	return color + padded + ansiReset
}

func rate(r engine.Result) float64 {
	if r.Elapsed <= 0 {
		return 0
	}
	return float64(r.Bytes) / (1024 * 1024) / r.Elapsed.Seconds()
}

// humanBytes renders a size the way a person reads it.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
