package logging

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"goft/internal/config"
)

// Outputs opens the places logs are written to — files, stdout, stderr — each
// once, however many loggers write there, and closes them together.
//
// One process runs several jobs, and two of them may name the same log file. A
// file is rotated by whoever writes it, and two writers rotating one file each
// believe they own it and pull it from under the other, so a file is opened
// once and shared. Each write is one whole record, made under the writer's own
// lock, so records from different jobs never interleave inside a line.
type Outputs struct {
	mu    sync.Mutex
	now   func() time.Time
	files map[string]io.WriteCloser // by config.PathKey

	stdout, stderr io.Writer
}

// NewOutputs returns an empty set of outputs. now is used for date based
// rotation; nil means time.Now.
func NewOutputs(now func() time.Time) *Outputs {
	if now == nil {
		now = time.Now
	}
	return &Outputs{
		now:    now,
		files:  map[string]io.WriteCloser{},
		stdout: &lockedWriter{w: os.Stdout},
		stderr: &lockedWriter{w: os.Stderr},
	}
}

// For returns the writer for a log section: its file, opened once and shared,
// or stdout — stderr when the human readable console has stdout.
//
// Every logger writing to one file shares its rotation, so the settings of the
// first to ask decide it; [config.Validate] has already refused a
// configuration where they would differ.
func (o *Outputs) For(l config.Log, console bool) (io.Writer, error) {
	switch {
	case l.Path != "":
		return o.file(l)
	case console:
		return o.stderr, nil
	default:
		return o.stdout, nil
	}
}

// Stdout is the shared, locked stdout, for the human readable output to write
// through so that its lines and those of a log on stdout never mix mid-line.
func (o *Outputs) Stdout() io.Writer { return o.stdout }

func (o *Outputs) file(l config.Log) (io.Writer, error) {
	path, err := filepath.Abs(l.Path)
	if err != nil {
		return nil, err
	}
	// Keyed so that two spellings of one file, Logs\jobs.log and
	// logs\jobs.log on Windows, share one writer rather than rotating the file
	// under each other.
	key := config.PathKey(path)
	o.mu.Lock()
	defer o.mu.Unlock()
	if w, ok := o.files[key]; ok {
		return w, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	l.Path = path
	w := newRotatingWriter(l, o.now)
	o.files[key] = w
	return w, nil
}

// Close closes every file that was opened.
func (o *Outputs) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	var errs []error
	for path, w := range o.files {
		errs = append(errs, w.Close())
		delete(o.files, path)
	}
	return errors.Join(errs...)
}

// lockedWriter makes each write to a shared stream whole, so that records and
// lines written from different goroutines never interleave.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
