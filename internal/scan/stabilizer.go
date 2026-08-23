package scan

import (
	"time"
)

// Stabilizer withholds a file until its size and modification time have stopped
// changing for stable_duration.
//
// Judging by modification time alone (now-mtime >= stable_duration) is not
// enough: tools that preserve timestamps, such as cp -p or rsync, leave the
// mtime at the source file's old value while the copy is still running, so a
// half-written file would look settled.
type Stabilizer struct {
	d   time.Duration
	now func() time.Time

	states  map[string]state
	changed map[string]bool
}

type state struct {
	size           int64
	modTime        time.Time
	unchangedSince time.Time
}

// NewStabilizer returns a stabilizer with the given settle duration. now may be
// nil, in which case time.Now is used.
func NewStabilizer(d time.Duration, now func() time.Time) *Stabilizer {
	if now == nil {
		now = time.Now
	}
	return &Stabilizer{d: d, now: now, states: map[string]state{}, changed: map[string]bool{}}
}

// Observe records the current listing and returns the files that have settled.
// Paths that disappeared from the listing are forgotten.
func (s *Stabilizer) Observe(files []File) []File {
	now := s.now()
	next := make(map[string]state, len(files))
	changed := make(map[string]bool, len(files))
	stable := make([]File, 0, len(files))

	for _, f := range files {
		st, seen := s.states[f.Path]
		switch {
		case !seen || st.size != f.Size || !st.modTime.Equal(f.ModTime):
			st = state{size: f.Size, modTime: f.ModTime, unchangedSince: now}
			changed[f.Path] = true
		default:
			// unchanged: keep the original timestamp so the wait accumulates
			// across cycles rather than restarting on every poll.
		}
		next[f.Path] = st
		if now.Sub(st.unchangedSince) >= s.d {
			stable = append(stable, f)
		}
	}

	s.states = next
	s.changed = changed
	return stable
}

// ChangedInLastCycle reports whether the most recent Observe saw this path for
// the first time, or saw its size or modification time change.
//
// Callers use it to report a recurring condition (an oversized file, say) once
// at warn level and at debug level on every cycle after that.
func (s *Stabilizer) ChangedInLastCycle(path string) bool { return s.changed[path] }

// Duration returns the configured settle duration.
func (s *Stabilizer) Duration() time.Duration { return s.d }
