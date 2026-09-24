package logging

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"

	"goft/internal/config"
)

// Layouts used for date based rotation. Free-form layouts are deliberately not
// accepted: a layout containing "/" or ":" would produce file names that are
// illegal on Windows.
const (
	layoutDaily   = "2006-01-02"
	layoutMonthly = "2006-01"
)

// Patterns matching what the layouts above produce, and the timestamp
// lumberjack appends to a backup it rotated out for size.
const (
	stampDaily   = `\d{4}-\d{2}-\d{2}`
	stampMonthly = `\d{4}-\d{2}`
	stampBackup  = `\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.\d{3}`
)

// newRotatingWriter returns the writer backing the log file.
//
// For rotation "size" this is a plain lumberjack logger. For "daily"/"monthly"
// it is a dateWriter that swaps the underlying lumberjack logger whenever the
// stamp changes, and prunes old generations itself.
func newRotatingWriter(cfg config.Log, now func() time.Time) io.WriteCloser {
	lj := func(name string) *lumberjack.Logger {
		return &lumberjack.Logger{
			Filename:   name,
			MaxSize:    cfg.MaxSizeMB,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAgeDays,
			Compress:   cfg.Compress,
			LocalTime:  true,
		}
	}

	if cfg.Rotation == config.RotationSize {
		return lj(cfg.Path)
	}

	layout, stamp := layoutDaily, stampDaily
	if cfg.Rotation == config.RotationMonthly {
		layout, stamp = layoutMonthly, stampMonthly
	}
	ext := filepath.Ext(cfg.Path)
	base := strings.TrimSuffix(filepath.Base(cfg.Path), ext)
	return &dateWriter{
		dir:        filepath.Dir(cfg.Path),
		base:       base,
		ext:        ext,
		layout:     layout,
		generation: generationPattern(base, ext, stamp),
		cfg:        cfg,
		now:        now,
		new:        lj,
	}
}

// generationPattern matches the files one dated log produces: the dated file
// itself, and the backups lumberjack rotates out of it for size, compressed or
// not.
//
// Matching on the base name as a prefix is not enough. Jobs commonly share a
// log directory, and one job's name can be the start of another's — invoice
// and invoice-archive — so a prefix match had the first job pruning the second
// one's logs as its own old generations. What follows the base name has to be
// a date in this writer's own layout.
func generationPattern(base, ext, stamp string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(base) + `-` + stamp +
		`(-` + stampBackup + `)?` + regexp.QuoteMeta(ext) + `(\.gz)?$`)
}

// dateWriter splits the log by calendar date on top of lumberjack's size based
// rotation. Because the base file name itself changes with the date, lumberjack
// can no longer see yesterday's files as its own backups, so retention has to
// be handled here.
type dateWriter struct {
	dir    string
	base   string
	ext    string
	layout string
	// generation recognises this writer's own files among everything else in
	// the directory, which is what pruning is allowed to touch.
	generation *regexp.Regexp
	cfg        config.Log
	now        func() time.Time
	new        func(name string) *lumberjack.Logger

	mu    sync.Mutex
	stamp string
	cur   *lumberjack.Logger
}

func (w *dateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	stamp := w.now().Format(w.layout)
	if w.cur == nil || stamp != w.stamp {
		if w.cur != nil {
			_ = w.cur.Close()
		}
		w.stamp = stamp
		w.cur = w.new(filepath.Join(w.dir, w.base+"-"+stamp+w.ext))
		w.prune()
	}
	return w.cur.Write(p)
}

func (w *dateWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur == nil {
		return nil
	}
	err := w.cur.Close()
	w.cur = nil
	return err
}

// prune deletes generations that fall outside max_backups / max_age_days.
// The file currently being written to is never a candidate.
func (w *dateWriter) prune() {
	if w.cfg.MaxBackups <= 0 && w.cfg.MaxAgeDays <= 0 {
		return
	}
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	active := w.base + "-" + w.stamp + w.ext

	type candidate struct {
		path string
		mod  time.Time
	}
	var found []candidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == active || !w.generation.MatchString(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		found = append(found, candidate{filepath.Join(w.dir, name), info.ModTime()})
	}

	// Sorted by name rather than by timestamp: the date stamp is part of the
	// file name, and several generations can share a modification time down to
	// the millisecond, which would make a time based order arbitrary.
	sort.Slice(found, func(i, j int) bool { return found[i].path > found[j].path })

	cutoff := w.now().AddDate(0, 0, -w.cfg.MaxAgeDays)
	for i, c := range found {
		tooMany := w.cfg.MaxBackups > 0 && i >= w.cfg.MaxBackups
		tooOld := w.cfg.MaxAgeDays > 0 && c.mod.Before(cutoff)
		if tooMany || tooOld {
			_ = os.Remove(c.path)
		}
	}
}
