package engine

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/fsys"
)

// foldingFS reports a destination that does not distinguish case, as a
// Windows or macOS disk or an SMB share does.
type foldingFS struct{ fsys.FS }

func (foldingFS) CaseInsensitive() bool { return true }

// extraListing is a source that reports entries the disk beneath it does not
// hold, with added appended to a directory's own listing and only replacing it.
//
// The case these tests are about needs a source offering two names that differ
// only in case, and a disk that folds case cannot be made to hold both — so on
// Windows and macOS the pair has to come from the listing rather than from the
// disk. Nothing reads a colliding file: the collision is settled from the scan,
// before the source is opened or stat'd, so an entry needs no bytes behind it.
type extraListing struct {
	fsys.FS
	added map[string][]fsys.Entry
	only  map[string][]fsys.Entry
}

func (e extraListing) List(ctx context.Context, dir string) ([]fsys.Entry, error) {
	if entries, ok := e.only[dir]; ok {
		return entries, nil
	}
	entries, err := e.FS.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	return append(entries, e.added[dir]...), nil
}

// listedFile is an entry for a regular file the listing claims to hold.
func listedFile(name string) fsys.Entry {
	return fsys.Entry{Name: name, Size: 6, ModTime: time.Now(), IsRegular: true}
}

// listedDir is an entry for a directory the listing claims to hold.
func listedDir(name string) fsys.Entry {
	return fsys.Entry{Name: name, ModTime: time.Now(), IsDir: true}
}

func TestNamesThatDifferOnlyInCaseAreNotMergedOnAFoldingDestination(t *testing.T) {
	// On a destination that folds case, A.csv and a.csv are one file. Sending
	// both would leave whichever arrived last, report both as delivered, and
	// with post_action: delete remove both sources: one of them silently lost.
	h := newHarness(t)
	h.cfg.OnExists = config.OnExistsOverwrite
	h.cfg.PostAction = config.PostDelete
	h.cfg.Retry.MaxAttempts = 3
	h.write(h.srcDir, "A.csv", "upper\n")
	h.write(h.srcDir, "b.csv", "unrelated\n")
	src := extraListing{FS: h.src, added: map[string][]fsys.Entry{"": {listedFile("a.csv")}}}

	runWith(t, h, src, foldingFS{h.dst})

	for _, name := range []string{"A.csv", "a.csv"} {
		r := h.result(name)
		if r.Outcome != Failed || !errors.Is(r.Err, fs.ErrInvalid) {
			t.Errorf("%s: result = %+v, want a failure naming the collision", name, r)
		}
		if r.Attempts != 1 {
			t.Errorf("%s: attempts = %d, want 1: the names collide however often it is tried", name, r.Attempts)
		}
		// Neither name may arrive, so neither is there to be found whether the
		// destination folds case or not.
		if h.exists(h.dstDir, name) {
			t.Errorf("%s: nothing should have been written for a colliding name", name)
		}
	}
	// post_action: delete is set, and a file that was not sent is not tidied
	// away. Only the real file can be checked for: on a folding disk the other
	// name is the same file.
	if !h.exists(h.srcDir, "A.csv") {
		t.Error("A.csv: the source must be kept")
	}
	// Files that do not collide are none of this business.
	if r := h.result("b.csv"); r.Outcome != Success {
		t.Errorf("b.csv: result = %+v, want it transferred", r)
	}
	if h.read(h.dstDir, "b.csv") != "unrelated\n" {
		t.Error("b.csv: the destination should hold it")
	}
}

func TestDirectoriesThatDifferOnlyInCaseCollideToo(t *testing.T) {
	// Whole paths are compared, so the collision can be in a directory rather
	// than in the file name.
	h := newHarness(t)
	h.cfg.Recursive = true
	h.write(h.srcDir, "Invoices/a.csv", "one\n")
	h.write(h.srcDir, "Invoices/b.csv", "three\n")
	src := extraListing{
		FS:    h.src,
		added: map[string][]fsys.Entry{"": {listedDir("invoices")}},
		only:  map[string][]fsys.Entry{"invoices": {listedFile("a.csv")}},
	}

	runWith(t, h, src, foldingFS{h.dst})

	for _, name := range []string{"Invoices/a.csv", "invoices/a.csv"} {
		if r := h.result(name); r.Outcome != Failed || !errors.Is(r.Err, fs.ErrInvalid) {
			t.Errorf("%s: result = %+v, want a failure", name, r)
		}
	}
	// A different file name under a directory that differs only in case merges
	// into the same directory there, which loses nothing.
	if r := h.result("Invoices/b.csv"); r.Outcome != Success {
		t.Errorf("Invoices/b.csv: result = %+v, want it transferred", r)
	}
}

func TestACaseSensitiveDestinationKeepsBoth(t *testing.T) {
	h := newHarness(t)
	if h.dst.CaseInsensitive() {
		t.Skip("this platform's disks fold case, which the other tests cover")
	}
	h.write(h.srcDir, "A.csv", "upper\n")
	h.write(h.srcDir, "a.csv", "lower\n")

	if s := h.run(); s.Succeeded != 2 {
		t.Errorf("summary = %+v, want both transferred where case is kept apart", s)
	}
}
