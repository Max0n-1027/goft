package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/fsys"
)

// slowMkdir makes directory creation take long enough for a second worker to
// look at the destination index while the first is still busy.
type slowMkdir struct {
	fsys.FS
	delay time.Duration
}

func (s slowMkdir) MkdirAll(ctx context.Context, dir string) error {
	time.Sleep(s.delay)
	return s.FS.MkdirAll(ctx, dir)
}

func TestWorkersWaitForADirectoryToBeCreated(t *testing.T) {
	h := newHarness(t)
	h.cfg.Workers = 4
	h.cfg.Recursive = true
	// Several files in one directory that the destination does not have yet,
	// which is what a first run into a fresh tree looks like.
	for _, n := range []string{"a", "b", "c", "d"} {
		h.write(h.srcDir, "fresh/"+n+".csv", n)
	}
	h.dst = fsys.NewErrFS(slowMkdir{FS: fsys.NewLocal(h.dstDir), delay: 80 * time.Millisecond})

	s := h.run()

	if s.Failed != 0 {
		t.Fatalf("summary = %+v, want every file transferred; a worker must not write into a directory another worker is still creating", s)
	}
	for _, n := range []string{"a", "b", "c", "d"} {
		if !h.exists(h.dstDir, "fresh/"+n+".csv") {
			t.Errorf("fresh/%s.csv is missing from the destination", n)
		}
	}
}

func TestPostActionFailureDoesNotResendTheFile(t *testing.T) {
	h := withRetry(newHarness(t), 3)
	h.cfg.PostAction = config.PostMove
	// A move target that already holds the name is a collision the archive
	// cannot resolve, so it fails every time.
	h.cfg.MoveTo = t.TempDir()
	h.write(h.srcDir, "a.csv", "hello")
	if err := os.WriteFile(filepath.Join(h.cfg.MoveTo, "a.csv"), []byte("older"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := h.run()

	if s.Failed != 1 {
		t.Fatalf("summary = %+v, want the file reported as failed", s)
	}
	// The file did arrive; only the tidying up went wrong. Sending 100 MB again
	// because a local move failed would be pointless.
	if got := h.read(h.dstDir, "a.csv"); got != "hello" {
		t.Errorf("destination = %q, want the file to have arrived", got)
	}
	if n := h.dst.Count(fsys.OpWrite); n != 1 {
		t.Errorf("the file was written %d times, want exactly one transfer", n)
	}
}

func TestPublishAvoidsRemovingWhenTheNameIsFree(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "hello")

	h.run()

	// Nothing is at the destination name, so a plain rename is enough. Removing
	// first would cost a round trip and, on a name that did exist, would leave
	// a moment with no file there at all.
	if n := h.dst.Count(fsys.OpRemove); n != 0 {
		t.Errorf("Remove called %d times, want none when the destination name is free", n)
	}
}
