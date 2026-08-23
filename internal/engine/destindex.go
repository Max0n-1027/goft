package engine

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync"

	"goft/internal/fsys"
)

// destIndex is a snapshot of the destination directories a cycle will write to.
//
// Looking every file up with Stat would cost one round trip per file on every
// cycle, which is what on_exists: skip runs into once the sending side keeps
// its already transferred files. Listing each directory once costs one round
// trip per directory instead.
type destIndex struct {
	caseInsensitive bool

	// files maps a directory to its entries and their sizes. When the
	// destination folds case a second, lower-cased map is kept alongside.
	files  map[string]map[string]int64
	folded map[string]map[string]int64

	mu      sync.Mutex
	missing map[string]bool // directories that do not exist yet
	created map[string]bool // directories this cycle has already created
}

func buildDestIndex(ctx context.Context, dst fsys.FS, dirs []string) (*destIndex, error) {
	idx := &destIndex{
		caseInsensitive: dst.CaseInsensitive(),
		files:           map[string]map[string]int64{},
		folded:          map[string]map[string]int64{},
		missing:         map[string]bool{},
		created:         map[string]bool{},
	}
	for _, dir := range dirs {
		entries, err := dst.List(ctx, dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				idx.missing[dir] = true
				continue
			}
			return nil, err
		}
		byName := make(map[string]int64, len(entries))
		var folded map[string]int64
		if idx.caseInsensitive {
			folded = make(map[string]int64, len(entries))
		}
		for _, e := range entries {
			if e.IsDir {
				continue
			}
			byName[e.Name] = e.Size
			if folded != nil {
				folded[strings.ToLower(e.Name)] = e.Size
			}
		}
		idx.files[dir] = byName
		if folded != nil {
			idx.folded[dir] = folded
		}
	}
	return idx, nil
}

// lookup reports the size of an existing destination file.
//
// Because the index is built from listings rather than from Stat, the operating
// system no longer resolves case for us. On destinations that fold case an
// exact miss is retried against the lower-cased names, so that FOO.CSV is
// recognised when foo.csv is being sent.
func (d *destIndex) lookup(dir, name string) (int64, bool) {
	if byName, ok := d.files[dir]; ok {
		if size, ok := byName[name]; ok {
			return size, true
		}
	}
	if !d.caseInsensitive {
		return 0, false
	}
	if folded, ok := d.folded[dir]; ok {
		if size, ok := folded[strings.ToLower(name)]; ok {
			return size, true
		}
	}
	return 0, false
}

// ensureDir creates dir on the destination the first time it is needed.
//
// The lock is held for the whole creation, not just for the bookkeeping. If it
// were released first, a second worker would see the directory marked as
// handled and start writing into it while the first was still creating it, and
// its write would fail with "no such file or directory". Directory creation
// happens at most once per directory per cycle, so serialising it costs
// nothing worth measuring.
func (d *destIndex) ensureDir(ctx context.Context, dst fsys.FS, dir string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.missing[dir] || d.created[dir] {
		return nil
	}
	if err := dst.MkdirAll(ctx, dir); err != nil {
		return err
	}
	d.created[dir] = true
	return nil
}
