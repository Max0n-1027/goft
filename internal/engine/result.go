/*
Package engine runs transfer cycles.

The engine is direction agnostic. It is handed a sending [fsys.FS] and a
receiving one and never learns which of them is local, so upload and download
are the same code path. Only the post-transfer move, which is upload-only, and
the arrow the console draws depend on the direction.

A cycle scans the sending side, waits for each file to stop changing, lists the
receiving directories once to decide what is already there, and then hands the
files to a pool of workers. Cycles run one at a time: [Engine.Serve] starts the
next one only after the previous has finished, which is what makes it
impossible for a file to be picked up again while it is still being sent, and
removes the need to track anything in flight.

Each file is written to the destination under a temporary name, verified, and
only then renamed onto its real name, so a name on the receiving side never
refers to a partial file. A failure is reported as a [Result] rather than
returned, so that one bad file does not end the cycle; failures that could
plausibly go differently next time are retried over a rebuilt connection, which
[Retryable] decides.
*/
package engine

import (
	"log/slog"
	"sort"
	"sync"
	"time"

	"goft/internal/fsys"
	"goft/internal/scan"
)

// Outcome is what happened to one file.
type Outcome string

// Possible outcomes.
const (
	Success Outcome = "success"
	Skipped Outcome = "skipped"
	Failed  Outcome = "failed"
)

// Reason explains a skip.
type Reason string

// Skip reasons.
const (
	ReasonAlreadyExists Reason = "already_exists"
	ReasonIdentical     Reason = "identical"
	ReasonSizeLimit     Reason = "size_limit"
)

// Result describes the handling of a single file.
type Result struct {
	// Index is the file's 1-based position within the cycle and Total the
	// number of files in it, so that progress can be shown as 3/12.
	Index int
	Total int
	// Path is the file relative to the sending root.
	Path string
	// Bytes is what was actually transferred, which for a file that was
	// skipped or that failed is the size it would have been. The cycle
	// [Summary] counts only the bytes that did move, so the two differ after a
	// failure and are meant to.
	Bytes int64
	// Elapsed covers the whole handling of the file, retries included.
	Elapsed time.Duration
	// Outcome is what happened, and Reason why, when a file was skipped.
	Outcome Outcome
	Reason  Reason
	// Err explains a failure.
	Err error
	// SrcHash and DstHash are the digests that were compared, valid only when
	// Hashed is set. They match on success by definition.
	SrcHash uint64
	DstHash uint64
	// Hashed marks a transfer that was verified by digest.
	Hashed bool
	// Attempts counts the tries this file took, including the one that worked.
	Attempts int
	// postActionFailed marks a file that arrived intact but whose source could
	// not be tidied up afterwards, so that it is not sent all over again.
	postActionFailed bool
	// Recurring marks a condition that was already reported on an earlier
	// cycle, so it can be logged once loudly and quietly from then on.
	Recurring bool
}

// sourceGone reports whether the post-transfer action took the file off the
// sending side.
//
// A skip for a file that is already at the destination is not one of these: no
// transfer happened, so the source was left alone, and the directory holding it
// is no emptier than it was.
func (r Result) sourceGone() bool {
	if r.postActionFailed {
		return false
	}
	return r.Outcome == Success || (r.Outcome == Skipped && r.Reason == ReasonIdentical)
}

// Level is the severity this result is reported at. Both the JSON log and the
// console use it, which is what keeps the single verbosity knob honest.
func (r Result) Level() slog.Level {
	switch {
	case r.Outcome == Failed:
		return slog.LevelError
	case r.Reason == ReasonSizeLimit && r.Recurring:
		return slog.LevelDebug
	case r.Reason == ReasonSizeLimit:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// Summary aggregates the results of one cycle.
type Summary struct {
	// Total is every file the cycle considered, and the three counts below
	// divide it up.
	Total     int
	Succeeded int
	Skipped   int
	Failed    int

	// Bytes counts only what was actually transferred.
	Bytes int64
	// Elapsed is how long the cycle took from scan to summary.
	Elapsed time.Duration
	// DirsRemoved counts the directories the cycle emptied and then removed,
	// which only happens with remove_empty_dirs.
	DirsRemoved int
	// PlannedOnly marks a dry run, where nothing was sent and the counts
	// describe what would have been.
	PlannedOnly bool
}

// Collector accumulates results from the workers.
type Collector struct {
	mu sync.Mutex
	s  Summary
	// emptied holds the directories a file was taken out of, which is where an
	// empty one can have appeared.
	emptied map[string]bool
}

// Add folds one result into the summary.
func (c *Collector) Add(r Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.s.Total++
	switch r.Outcome {
	case Success:
		c.s.Succeeded++
		c.s.Bytes += r.Bytes
	case Skipped:
		c.s.Skipped++
	case Failed:
		c.s.Failed++
	}
	if r.sourceGone() {
		if dir := fsys.Dir(r.Path); dir != "" {
			if c.emptied == nil {
				c.emptied = map[string]bool{}
			}
			c.emptied[dir] = true
		}
	}
}

// Emptied lists the directories the cycle took a file out of, deepest first so
// that a parent is considered only once its children have been dealt with.
//
// Every directory on the way up to the root is included, because removing the
// last subdirectory of a directory empties that one too.
func (c *Collector) Emptied() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	all := map[string]bool{}
	for dir := range c.emptied {
		for d := dir; d != ""; d = fsys.Dir(d) {
			all[d] = true
		}
	}

	dirs := make([]string, 0, len(all))
	for d := range all {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool {
		di, dj := len(fsys.Segments(dirs[i])), len(fsys.Segments(dirs[j]))
		if di != dj {
			return di > dj
		}
		return dirs[i] < dirs[j]
	})
	return dirs
}

// Summary returns the aggregate so far.
func (c *Collector) Summary() Summary {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.s
}

// Plan is the set of files a cycle is about to handle, reported before any
// transfer starts so that the console can print a header.
type Plan struct {
	// Files are the transfer candidates, and Bytes their combined size.
	Files []scan.File
	Bytes int64
	// DryRun marks a plan that will not be acted on.
	DryRun bool
}
