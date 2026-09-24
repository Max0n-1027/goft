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

// appendOnOpen appends to a file on the sending side whenever the wrapped FS
// opens something, standing in for a writer that is still at work. Which FS it
// wraps decides when the change lands: the source's Open is the read for the
// transfer, the destination's is the read back for verification.
type appendOnOpen struct {
	fsys.FS
	path string
}

func (a appendOnOpen) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString("a line written after the file had settled\n"); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return a.FS.Open(ctx, name)
}

func runWith(t *testing.T, h *harness, src, dst fsys.FS) {
	t.Helper()
	h.results = nil
	e := New(Options{
		Config:    h.cfg,
		Direction: config.DirSend,
		NewSrc:    func(context.Context) (fsys.FS, error) { return src, nil },
		NewDst:    func(context.Context) (fsys.FS, error) { return dst, nil },
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Single:    true,
		OnResult:  func(r Result) { h.results = append(h.results, r) },
	})
	if _, err := e.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

func TestASourceThatGrowsBeforeItIsReadIsNotPublished(t *testing.T) {
	// The file settled, then its writer carried on. What was read is no longer
	// the file that settled, so it must not appear under the real name, where
	// a consumer would take it for complete.
	h := newHarness(t)
	h.cfg.PostAction = config.PostDelete
	h.cfg.Retry.MaxAttempts = 3
	h.write(h.srcDir, "invoice.csv", "id,amount\n")
	src := appendOnOpen{FS: h.src, path: filepath.Join(h.srcDir, "invoice.csv")}

	runWith(t, h, src, h.dst)

	r := h.result("invoice.csv")
	if r.Outcome != Failed || !errors.Is(r.Err, ErrSourceChanged) {
		t.Fatalf("result = %+v, want a failure for a source that changed", r)
	}
	// Retrying at once would find the writer still at work; the next cycle's
	// settling is what decides when the file is ready.
	if r.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", r.Attempts)
	}
	if h.exists(h.dstDir, "invoice.csv") || h.exists(h.dstDir, "invoice.csv"+fsys.TempSuffix) {
		t.Error("nothing should have been left at the destination")
	}
	if !h.exists(h.srcDir, "invoice.csv") {
		t.Error("the source must not be deleted")
	}
}

func TestASourceThatGrowsAfterItWasSentIsNotDeleted(t *testing.T) {
	// The bytes that were sent arrived intact and verified, but the writer
	// appended more while that was happening. Deleting the source now would
	// throw away data that never reached the destination.
	h := newHarness(t)
	h.cfg.PostAction = config.PostDelete
	h.write(h.srcDir, "invoice.csv", "id,amount\n")
	dst := appendOnOpen{FS: h.dst, path: filepath.Join(h.srcDir, "invoice.csv")}

	runWith(t, h, h.src, dst)

	r := h.result("invoice.csv")
	if r.Outcome != Failed || !errors.Is(r.Err, ErrSourceChanged) {
		t.Fatalf("result = %+v, want a failure for a source that changed", r)
	}
	if r.Attempts != 1 {
		t.Errorf("attempts = %d, want 1: the file is not sent again", r.Attempts)
	}
	if !h.exists(h.srcDir, "invoice.csv") {
		t.Fatal("the source was deleted with data in it that was never sent")
	}
}

func TestASourceThatChangesAfterComparingIsNotDeleted(t *testing.T) {
	// on_exists: overwrite found the destination identical, which makes the
	// source eligible for its post-transfer action. It grew in the meantime,
	// so the comparison no longer describes it.
	h := newHarness(t)
	h.cfg.OnExists = config.OnExistsOverwrite
	h.cfg.PostAction = config.PostDelete
	h.write(h.srcDir, "invoice.csv", "id,amount\n")
	h.write(h.dstDir, "invoice.csv", "id,amount\n")
	dst := appendOnOpen{FS: h.dst, path: filepath.Join(h.srcDir, "invoice.csv")}

	runWith(t, h, h.src, dst)

	if r := h.result("invoice.csv"); !errors.Is(r.Err, ErrSourceChanged) {
		t.Fatalf("result = %+v, want a failure for a source that changed", r)
	}
	if !h.exists(h.srcDir, "invoice.csv") {
		t.Fatal("the source was deleted after changing")
	}
}

func TestAnUnchangedSourceIsStillDeleted(t *testing.T) {
	h := newHarness(t)
	h.cfg.PostAction = config.PostDelete
	h.write(h.srcDir, "invoice.csv", "id,amount\n")

	if s := h.run(); s.Succeeded != 1 {
		t.Fatalf("summary = %+v, want the file transferred", s)
	}
	if h.exists(h.srcDir, "invoice.csv") {
		t.Error("the source should have been deleted")
	}
}
