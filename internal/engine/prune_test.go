package engine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"goft/internal/config"
	"goft/internal/fsys"
)

// newPruning is a harness that empties the source as it goes and removes the
// directories that leaves behind.
func newPruning(t *testing.T) *harness {
	h := newHarness(t)
	h.cfg.Recursive = true
	h.cfg.PostAction = config.PostDelete
	h.cfg.RemoveEmptyDirs = true
	return h
}

func TestEmptiedDirectoriesAreRemoved(t *testing.T) {
	h := newPruning(t)
	h.write(h.srcDir, "2026-08/invoice.csv", "id\n")

	s := h.run()
	if s.DirsRemoved != 1 {
		t.Errorf("DirsRemoved = %d, want 1", s.DirsRemoved)
	}
	if h.exists(h.srcDir, "2026-08") {
		t.Error("the directory the transfer emptied should have been removed")
	}
	// The file still had to arrive: tidying up is not the job.
	if !h.exists(h.dstDir, "2026-08/invoice.csv") {
		t.Error("the file should have been transferred")
	}
}

func TestRemovalWalksUpAsFarAsItEmpties(t *testing.T) {
	// Taking the last file out of 2026-08/day-01 empties 2026-08 as well, and
	// the point of the option is not to leave that behind either.
	h := newPruning(t)
	h.write(h.srcDir, "2026-08/day-01/invoice.csv", "id\n")

	if s := h.run(); s.DirsRemoved != 2 {
		t.Errorf("DirsRemoved = %d, want both levels removed", s.DirsRemoved)
	}
	if h.exists(h.srcDir, "2026-08") {
		t.Error("the parent should have gone too, being empty now")
	}
	if !h.exists(h.srcDir, "") {
		t.Error("the sending root must never be removed: the job watches it")
	}
}

func TestRemovalStopsWhereSomethingIsLeft(t *testing.T) {
	h := newPruning(t)
	h.cfg.Include = []string{"*.csv"}
	h.write(h.srcDir, "2026-08/invoice.csv", "id\n")
	// Not a transfer candidate, so the directory does not become empty.
	h.write(h.srcDir, "2026-08/notes.txt", "kept")

	if s := h.run(); s.DirsRemoved != 0 {
		t.Errorf("DirsRemoved = %d, want none: the directory still holds a file", s.DirsRemoved)
	}
	if !h.exists(h.srcDir, "2026-08/notes.txt") {
		t.Error("a file that was never transferred must not be removed with the directory")
	}
	// Only the transferred file was removed. The directory is listed first and
	// left alone, rather than the removal being attempted and left to the
	// operating system to refuse — which not every protocol would.
	if got := h.src.Count(fsys.OpRemove); got != 1 {
		t.Errorf("Remove was called %d times, want only the transferred file", got)
	}
}

func TestOnlyDirectoriesTheCycleEmptiedAreRemoved(t *testing.T) {
	// An empty directory that was already there is none of goft's business:
	// it was not emptied by this transfer, and something else may be filling
	// it.
	h := newPruning(t)
	h.write(h.srcDir, "2026-08/invoice.csv", "id\n")
	if err := os.MkdirAll(filepath.Join(h.srcDir, "2026-09"), 0o755); err != nil {
		t.Fatal(err)
	}

	h.run()
	if !h.exists(h.srcDir, "2026-09") {
		t.Error("a directory this cycle did not empty should have been left alone")
	}
}

func TestSkippedFilesLeaveTheirDirectoryAlone(t *testing.T) {
	// on_exists: skip means no transfer happened, so the source file stays and
	// the directory is no emptier than it was.
	h := newPruning(t)
	h.write(h.srcDir, "2026-08/invoice.csv", "id\n")
	h.write(h.dstDir, "2026-08/invoice.csv", "id\n")

	if s := h.run(); s.DirsRemoved != 0 {
		t.Errorf("DirsRemoved = %d, want none", s.DirsRemoved)
	}
	if !h.exists(h.srcDir, "2026-08/invoice.csv") {
		t.Error("a skipped file must keep its source")
	}
	if !h.exists(h.srcDir, "2026-08") {
		t.Error("the directory of a skipped file must survive")
	}
}

func TestPruningIsOffByDefault(t *testing.T) {
	h := newHarness(t)
	h.cfg.Recursive = true
	h.cfg.PostAction = config.PostDelete
	h.write(h.srcDir, "2026-08/invoice.csv", "id\n")

	if s := h.run(); s.DirsRemoved != 0 {
		t.Errorf("DirsRemoved = %d, want none without remove_empty_dirs", s.DirsRemoved)
	}
	if !h.exists(h.srcDir, "2026-08") {
		t.Error("the directory should have been left behind")
	}
}

func TestADirectoryThatCannotBeRemovedIsOnlyAWarning(t *testing.T) {
	// The files reached the destination, which is the job. A directory that
	// could not be tidied away is tried again on the next cycle rather than
	// turning a delivered transfer into a failure.
	h := newPruning(t)
	if err := os.MkdirAll(filepath.Join(h.srcDir, "2026-08"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.src.FailOp(fsys.OpRemove, errors.New("directory not empty"))

	e := New(Options{
		Config:    h.cfg,
		Direction: config.DirSend,
		NewSrc:    func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst:    func(context.Context) (fsys.FS, error) { return h.dst, nil },
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})

	c := &conn{e: e, ctx: context.Background(), src: h.src, dst: h.dst}
	if removed := e.pruneEmptied(context.Background(), c, []string{"2026-08"}); removed != 0 {
		t.Errorf("removed = %d, want none: the removal failed", removed)
	}
	if !h.exists(h.srcDir, "2026-08") {
		t.Error("the directory should still be there")
	}
}

func TestPruningLeavesADirectoryThatFilledUpAgain(t *testing.T) {
	// Between the transfer and the tidying up, something else may have written
	// to the directory. It is listed again rather than trusted to be empty.
	h := newPruning(t)
	h.write(h.srcDir, "2026-08/late-arrival.csv", "id\n")

	e := New(Options{
		Config:    h.cfg,
		Direction: config.DirSend,
		NewSrc:    func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst:    func(context.Context) (fsys.FS, error) { return h.dst, nil },
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})

	c := &conn{e: e, ctx: context.Background(), src: h.src, dst: h.dst}
	if removed := e.pruneEmptied(context.Background(), c, []string{"2026-08"}); removed != 0 {
		t.Errorf("removed = %d, want none: the directory is not empty", removed)
	}
	if !h.exists(h.srcDir, "2026-08/late-arrival.csv") {
		t.Error("the file that arrived late should still be there")
	}
}
