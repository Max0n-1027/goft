package engine

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/fsys"
)

// harness drives the engine over two local directories. The engine is direction
// agnostic, so exercising it local-to-local covers everything except the
// protocol implementations themselves.
type harness struct {
	t       *testing.T
	srcDir  string
	dstDir  string
	src     *fsys.ErrFS
	dst     *fsys.ErrFS
	cfg     *config.Config
	results []Result

	mu       sync.Mutex
	srcOpens int
	dstOpens int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:      t,
		srcDir: t.TempDir(),
		dstDir: t.TempDir(),
	}
	h.src = fsys.NewErrFS(fsys.NewLocal(h.srcDir))
	h.dst = fsys.NewErrFS(fsys.NewLocal(h.dstDir))
	h.cfg = &config.Config{
		Name:       "test",
		Local:      config.Local{Path: h.srcDir},
		Remote:     config.Remote{Protocol: config.ProtocolSFTP, Host: "h", Path: "/p"},
		Include:    []string{"*"},
		Verify:     config.VerifyHash,
		OnExists:   config.OnExistsSkip,
		PostAction: config.PostNone,
		Workers:    1,
		// Settling is exercised in the scan package; here it only gets in the
		// way, so files are eligible as soon as they are seen.
		StableDuration: 0,
		PollInterval:   time.Second,
		// Retrying is exercised on its own; elsewhere it would only slow the
		// tests down and hide the first failure.
		Retry: config.Retry{MaxAttempts: 1, Interval: 0, Backoff: 1},
	}
	return h
}

func (h *harness) write(dir, name, body string) {
	h.t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) read(dir, name string) string {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		h.t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func (h *harness) exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil
}

func (h *harness) run() Summary {
	h.t.Helper()
	h.results = nil
	e := New(Options{
		Config:    h.cfg,
		Direction: config.DirSend,
		NewSrc: func(context.Context) (fsys.FS, error) {
			h.mu.Lock()
			h.srcOpens++
			h.mu.Unlock()
			return h.src, nil
		},
		NewDst: func(context.Context) (fsys.FS, error) {
			h.mu.Lock()
			h.dstOpens++
			h.mu.Unlock()
			return h.dst, nil
		},
		Logger:   slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo})),
		Single:   true,
		OnResult: func(r Result) { h.results = append(h.results, r) },
	})
	s, err := e.RunOnce(context.Background())
	if err != nil {
		h.t.Fatalf("RunOnce: %v", err)
	}
	return s
}

func (h *harness) result(path string) Result {
	h.t.Helper()
	for _, r := range h.results {
		if r.Path == path {
			return r
		}
	}
	h.t.Fatalf("no result reported for %q", path)
	return Result{}
}

func TestTransfersAndVerifies(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "hello")

	s := h.run()
	if s.Succeeded != 1 || s.Failed != 0 {
		t.Fatalf("summary = %+v, want one success", s)
	}
	if got := h.read(h.dstDir, "a.csv"); got != "hello" {
		t.Errorf("destination content = %q, want %q", got, "hello")
	}
	if h.exists(h.dstDir, "a.csv"+fsys.TempSuffix) {
		t.Error("the temporary file should have been renamed away")
	}
}

func TestNestedPathsAreRecreated(t *testing.T) {
	h := newHarness(t)
	h.cfg.Recursive = true
	h.write(h.srcDir, "2026/08/a.csv", "x")

	h.run()
	if !h.exists(h.dstDir, "2026/08/a.csv") {
		t.Error("the destination directory tree should have been created")
	}
}

func TestSkipLeavesTheSourceAlone(t *testing.T) {
	h := newHarness(t)
	h.cfg.PostAction = config.PostDelete
	h.write(h.srcDir, "a.csv", "new")
	h.write(h.dstDir, "a.csv", "old")

	h.run()

	if got := h.read(h.dstDir, "a.csv"); got != "old" {
		t.Errorf("destination = %q, want it untouched under on_exists: skip", got)
	}
	// skip means the transfer did not happen, so post-processing must not run
	// either; deleting here would lose a file that was never sent.
	if !h.exists(h.srcDir, "a.csv") {
		t.Error("the source must survive a skip, even with post_action: delete")
	}
	if r := h.result("a.csv"); r.Reason != ReasonAlreadyExists {
		t.Errorf("reason = %q, want %q", r.Reason, ReasonAlreadyExists)
	}
}

func TestOverwriteSkipsIdenticalButStillArchives(t *testing.T) {
	h := newHarness(t)
	h.cfg.OnExists = config.OnExistsOverwrite
	h.cfg.PostAction = config.PostDelete
	h.write(h.srcDir, "a.csv", "same")
	h.write(h.dstDir, "a.csv", "same")

	h.run()

	if r := h.result("a.csv"); r.Outcome != Skipped || r.Reason != ReasonIdentical {
		t.Errorf("result = %+v, want a skip because the file is already identical", r)
	}
	// The destination already holds the right bytes, so as far as the source is
	// concerned the transfer is done and post-processing applies.
	if h.exists(h.srcDir, "a.csv") {
		t.Error("an identical file counts as transferred, so post_action should have run")
	}
}

func TestOverwriteReplacesDifferentContent(t *testing.T) {
	h := newHarness(t)
	h.cfg.OnExists = config.OnExistsOverwrite
	h.write(h.srcDir, "a.csv", "new")
	h.write(h.dstDir, "a.csv", "old")

	h.run()

	if got := h.read(h.dstDir, "a.csv"); got != "new" {
		t.Errorf("destination = %q, want it overwritten", got)
	}
}

func TestVerifyNoneAlwaysOverwrites(t *testing.T) {
	h := newHarness(t)
	h.cfg.OnExists = config.OnExistsOverwrite
	h.cfg.Verify = config.VerifyNone
	h.write(h.srcDir, "a.csv", "same")
	h.write(h.dstDir, "a.csv", "same")

	h.run()

	if r := h.result("a.csv"); r.Outcome != Success {
		t.Errorf("result = %+v: without verification there is no way to know the files match, so it must transfer", r)
	}
}

func TestFailedWriteLeavesTheDestinationIntact(t *testing.T) {
	h := newHarness(t)
	h.cfg.OnExists = config.OnExistsOverwrite
	h.write(h.srcDir, "a.csv", "new")
	h.write(h.dstDir, "a.csv", "old")
	h.dst.FailOp(fsys.OpWrite, errors.New("disk full"))

	s := h.run()

	if s.Failed != 1 {
		t.Fatalf("summary = %+v, want one failure", s)
	}
	if got := h.read(h.dstDir, "a.csv"); got != "old" {
		t.Errorf("destination = %q: a failed transfer must not damage the file that was already there", got)
	}
	if h.exists(h.dstDir, "a.csv"+fsys.TempSuffix) {
		t.Error("the temporary file should have been cleaned up")
	}
}

func TestLeftoverTemporaryFileIsReused(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "hello")
	// What a killed process leaves behind.
	h.write(h.dstDir, "a.csv"+fsys.TempSuffix, "garbage from a previous run")

	h.run()

	if got := h.read(h.dstDir, "a.csv"); got != "hello" {
		t.Errorf("destination = %q, want the fresh transfer", got)
	}
	if h.exists(h.dstDir, "a.csv"+fsys.TempSuffix) {
		t.Error("the stale temporary file should be gone")
	}
}

func TestSourceIsNeverScannedForTemporaryFiles(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv"+fsys.TempSuffix, "in flight")

	s := h.run()
	if s.Total != 0 {
		t.Errorf("summary = %+v, want goft's own temporary files ignored", s)
	}
}

func TestSizeLimitSkipsWithoutFailing(t *testing.T) {
	h := newHarness(t)
	h.cfg.MaxFileSizeMB = 1
	h.write(h.srcDir, "small.csv", "tiny")
	h.write(h.srcDir, "big.dat", strings.Repeat("x", 2*1024*1024))

	s := h.run()

	if s.Failed != 0 {
		t.Errorf("summary = %+v: an oversized file is skipped, not failed", s)
	}
	if r := h.result("big.dat"); r.Outcome != Skipped || r.Reason != ReasonSizeLimit {
		t.Errorf("result = %+v, want a size_limit skip", r)
	}
	if r := h.result("big.dat"); r.Level() != slog.LevelWarn {
		t.Errorf("level = %v, want warn the first time a file is rejected", r.Level())
	}
	if !h.exists(h.dstDir, "small.csv") {
		t.Error("the other file should still have been transferred")
	}
	if h.exists(h.dstDir, "big.dat") {
		t.Error("the oversized file must not be transferred")
	}
}

func TestExistenceIsCheckedByListingNotByStat(t *testing.T) {
	h := newHarness(t)
	for _, n := range []string{"a", "b", "c", "d"} {
		h.write(h.srcDir, n+".csv", n)
	}

	h.run()

	if got := h.dst.Count(fsys.OpList); got != 1 {
		t.Errorf("List called %d times, want one listing for the single destination directory", got)
	}
	// Stat is only for the re-check just before the rename, so it scales with
	// files actually transferred rather than with files examined.
	if got := h.dst.Count(fsys.OpStat); got > 4 {
		t.Errorf("Stat called %d times, want at most one per transferred file", got)
	}
}

func TestFileAppearingMidCycleIsNotOverwritten(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "mine")

	// The destination listing is a snapshot; another process may create the
	// file while the transfer is in flight. Under skip, that file wins.
	h.dst.OverrideStat("a.csv", func() (fsys.FileInfo, error) {
		return fsys.FileInfo{Size: 5}, nil
	})

	h.run()

	if r := h.result("a.csv"); r.Outcome != Skipped || r.Reason != ReasonAlreadyExists {
		t.Errorf("result = %+v, want a skip rather than an overwrite", r)
	}
	if h.exists(h.dstDir, "a.csv") {
		t.Error("the transfer should have been abandoned instead of publishing")
	}
	if h.exists(h.dstDir, "a.csv"+fsys.TempSuffix) {
		t.Error("the temporary file should have been cleaned up")
	}
}

func TestConnectionsNeverExceedWorkers(t *testing.T) {
	h := newHarness(t)
	h.cfg.Workers = 3
	for i := 0; i < 12; i++ {
		h.write(h.srcDir, string(rune('a'+i))+".csv", "x")
	}

	h.run()

	if h.srcOpens > 3 || h.dstOpens > 3 {
		t.Errorf("opened %d source and %d destination connections, want at most workers (3)",
			h.srcOpens, h.dstOpens)
	}
}

func TestDryRunTouchesNothing(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "hello")

	var plan Plan
	e := New(Options{
		Config:    h.cfg,
		Direction: config.DirSend,
		NewSrc:    func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst: func(context.Context) (fsys.FS, error) {
			t.Error("a dry run must not connect to the destination")
			return h.dst, nil
		},
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Single: true,
		DryRun: true,
		OnPlan: func(p Plan) { plan = p },
	})
	s, err := e.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if !s.PlannedOnly || s.Total != 1 {
		t.Errorf("summary = %+v, want one planned file and nothing transferred", s)
	}
	if len(plan.Files) != 1 || plan.Files[0].Path != "a.csv" {
		t.Errorf("plan = %+v, want it to list the candidate", plan)
	}
	if h.exists(h.dstDir, "a.csv") {
		t.Error("nothing should have been written")
	}
}

func TestCaseInsensitiveDestinationRecognisesExistingFiles(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "invoice.csv", "new")
	h.write(h.dstDir, "INVOICE.CSV", "old")

	// Existence is decided from a listing, so on a destination that folds case
	// the comparison has to fold it too, or the same file is sent twice under
	// two spellings.
	h.dst = fsys.NewErrFS(caseFolding{fsys.NewLocal(h.dstDir)})

	if r := h.runAndGet("invoice.csv"); r.Reason != ReasonAlreadyExists {
		t.Errorf("result = %+v, want the differently cased file recognised", r)
	}
}

func (h *harness) runAndGet(path string) Result {
	h.run()
	return h.result(path)
}

// caseFolding presents a local directory as a case insensitive file system,
// the way SMB and Windows behave.
type caseFolding struct{ fsys.FS }

func (c caseFolding) CaseInsensitive() bool { return true }

func TestMissingSourceDirectoryIsReported(t *testing.T) {
	h := newHarness(t)
	h.src = fsys.NewErrFS(fsys.NewLocal(filepath.Join(h.srcDir, "gone")))

	e := New(Options{
		Config:    h.cfg,
		Direction: config.DirSend,
		NewSrc:    func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst:    func(context.Context) (fsys.FS, error) { return h.dst, nil },
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Single:    true,
	})
	if _, err := e.RunOnce(context.Background()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("RunOnce() error = %v, want it to surface the missing directory", err)
	}
}
