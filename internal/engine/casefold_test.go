package engine

import (
	"errors"
	"io/fs"
	"testing"

	"goft/internal/config"
	"goft/internal/fsys"
)

// foldingFS reports a destination that does not distinguish case, as a
// Windows or macOS disk or an SMB share does.
type foldingFS struct{ fsys.FS }

func (foldingFS) CaseInsensitive() bool { return true }

func TestNamesThatDifferOnlyInCaseAreNotMergedOnAFoldingDestination(t *testing.T) {
	// On a destination that folds case, A.csv and a.csv are one file. Sending
	// both would leave whichever arrived last, report both as delivered, and
	// with post_action: delete remove both sources: one of them silently lost.
	h := newHarness(t)
	h.cfg.OnExists = config.OnExistsOverwrite
	h.cfg.PostAction = config.PostDelete
	h.cfg.Retry.MaxAttempts = 3
	h.write(h.srcDir, "A.csv", "upper\n")
	h.write(h.srcDir, "a.csv", "lower\n")
	h.write(h.srcDir, "b.csv", "unrelated\n")

	runWith(t, h, h.src, foldingFS{h.dst})

	for _, name := range []string{"A.csv", "a.csv"} {
		r := h.result(name)
		if r.Outcome != Failed || !errors.Is(r.Err, fs.ErrInvalid) {
			t.Errorf("%s: result = %+v, want a failure naming the collision", name, r)
		}
		if r.Attempts != 1 {
			t.Errorf("%s: attempts = %d, want 1: the names collide however often it is tried", name, r.Attempts)
		}
		if !h.exists(h.srcDir, name) {
			t.Errorf("%s: the source must be kept", name)
		}
		if h.exists(h.dstDir, name) {
			t.Errorf("%s: nothing should have been written for a colliding name", name)
		}
	}
	// Files that do not collide are none of this business.
	if r := h.result("b.csv"); r.Outcome != Success {
		t.Errorf("b.csv: result = %+v, want it transferred", r)
	}
}

func TestDirectoriesThatDifferOnlyInCaseCollideToo(t *testing.T) {
	h := newHarness(t)
	h.cfg.Recursive = true
	h.write(h.srcDir, "Invoices/a.csv", "one\n")
	h.write(h.srcDir, "invoices/a.csv", "two\n")
	h.write(h.srcDir, "invoices/b.csv", "three\n")

	runWith(t, h, h.src, foldingFS{h.dst})

	for _, name := range []string{"Invoices/a.csv", "invoices/a.csv"} {
		if r := h.result(name); r.Outcome != Failed {
			t.Errorf("%s: result = %+v, want a failure", name, r)
		}
	}
	// A different file name in a directory that differs only in case merges
	// into the same directory there, which loses nothing.
	if r := h.result("invoices/b.csv"); r.Outcome != Success {
		t.Errorf("invoices/b.csv: result = %+v, want it transferred", r)
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
