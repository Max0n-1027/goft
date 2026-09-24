package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"strings"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/fsys"
)

// withRetry turns retrying on for a harness, with no waiting so the tests stay
// quick; the waiting itself is covered by config.Retry.Wait.
func withRetry(h *harness, attempts int) *harness {
	h.cfg.Retry = config.Retry{MaxAttempts: attempts, Interval: 0, Backoff: 1}
	return h
}

func TestTransientFailureSucceedsOnRetry(t *testing.T) {
	h := withRetry(newHarness(t), 3)
	h.write(h.srcDir, "a.csv", "hello")
	h.dst.FailOpTimes(fsys.OpWrite, errors.New("connection reset by peer"), 1)

	s := h.run()

	if s.Succeeded != 1 || s.Failed != 0 {
		t.Fatalf("summary = %+v, want the retry to have carried the file", s)
	}
	if r := h.result("a.csv"); r.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", r.Attempts)
	}
	if got := h.read(h.dstDir, "a.csv"); got != "hello" {
		t.Errorf("destination = %q, want hello", got)
	}
}

func TestRetryGivesUpAfterMaxAttempts(t *testing.T) {
	h := withRetry(newHarness(t), 3)
	h.write(h.srcDir, "a.csv", "hello")
	h.dst.FailOp(fsys.OpWrite, errors.New("connection reset by peer"))

	s := h.run()

	if s.Failed != 1 {
		t.Fatalf("summary = %+v, want the file to fail", s)
	}
	if r := h.result("a.csv"); r.Attempts != 3 {
		t.Errorf("attempts = %d, want the configured maximum of 3", r.Attempts)
	}
}

func TestPermanentFailureIsNotRetried(t *testing.T) {
	h := withRetry(newHarness(t), 3)
	h.write(h.srcDir, "a.csv", "hello")
	// Permission denied will be denied again in two seconds' time.
	h.dst.FailOp(fsys.OpWrite, fmt.Errorf("create: %w", fs.ErrPermission))

	s := h.run()

	if s.Failed != 1 {
		t.Fatalf("summary = %+v, want the file to fail", s)
	}
	if r := h.result("a.csv"); r.Attempts != 1 {
		t.Errorf("attempts = %d, want a single attempt for an error that cannot improve", r.Attempts)
	}
}

func TestVerificationFailureIsRetried(t *testing.T) {
	h := withRetry(newHarness(t), 2)
	h.write(h.srcDir, "a.csv", "hello")
	// Failing the read-back breaks verification. Verification failures are
	// retried on purpose: a digest that does not match can come from corruption
	// in flight, which is exactly what a second attempt fixes.
	h.dst.FailOpTimes(fsys.OpOpen, errors.New("short read"), 1)

	s := h.run()

	if s.Succeeded != 1 {
		t.Fatalf("summary = %+v, want the second attempt to succeed", s)
	}
	if r := h.result("a.csv"); r.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", r.Attempts)
	}
}

func TestSingleAttemptDisablesRetrying(t *testing.T) {
	h := withRetry(newHarness(t), 1)
	h.write(h.srcDir, "a.csv", "hello")
	h.dst.FailOpTimes(fsys.OpWrite, errors.New("connection reset by peer"), 1)

	s := h.run()

	if s.Failed != 1 {
		t.Errorf("summary = %+v, want max_attempts 1 to mean no second try", s)
	}
}

func TestConnectionIsRebuiltBetweenAttempts(t *testing.T) {
	h := withRetry(newHarness(t), 2)
	h.write(h.srcDir, "a.csv", "hello")
	h.dst.FailOpTimes(fsys.OpWrite, errors.New("broken pipe"), 1)

	h.run()

	// One connection to start with, one more after the failure. Retrying over
	// a connection that just died would fail the same way.
	if h.dstOpens != 2 || h.srcOpens != 2 {
		t.Errorf("opened %d source and %d destination connections, want 2 of each",
			h.srcOpens, h.dstOpens)
	}
}

func TestRetryStopsWhenTheRunIsCancelled(t *testing.T) {
	h := newHarness(t)
	h.cfg.Retry = config.Retry{MaxAttempts: 5, Interval: 30 * time.Second, Backoff: 1}
	h.write(h.srcDir, "a.csv", "hello")
	h.dst.FailOp(fsys.OpWrite, errors.New("connection reset by peer"))

	ctx, cancel := context.WithCancel(context.Background())
	e := New(Options{
		Config:    h.cfg,
		Direction: config.DirSend,
		NewSrc:    func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst:    func(context.Context) (fsys.FS, error) { return h.dst, nil },
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Single:    true,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.RunOnce(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a shutdown must not have to wait out the retry interval")
	}
}

func TestRetryIsRecordedInTheLog(t *testing.T) {
	h := withRetry(newHarness(t), 3)
	h.write(h.srcDir, "a.csv", "hello")
	h.dst.FailOpTimes(fsys.OpWrite, errors.New("connection reset by peer"), 1)

	records := runAtLevel(t, h, slog.LevelInfo)

	var sawRetry, sawAttempts bool
	for _, r := range records {
		if r["msg"] == "retrying transfer" {
			sawRetry = true
		}
		if r["result"] == "success" {
			if n, ok := r["attempt"].(float64); ok && n == 2 {
				sawAttempts = true
			}
		}
	}
	if !sawRetry {
		t.Error("a retry should be visible in the log, not silent")
	}
	if !sawAttempts {
		t.Error("the successful record should say how many attempts it took")
	}
}

func TestAnArchiveCollisionIsNotRetried(t *testing.T) {
	// A name already taken under move_to will still be taken a moment later.
	// Retrying it only spent the backoff on every such file, every cycle, and
	// wrote a retry warning each time.
	h := newHarness(t)
	archive := t.TempDir()
	h.cfg.PostAction = config.PostMove
	h.cfg.MoveTo = archive
	h.cfg.Retry = config.Retry{MaxAttempts: 3, Interval: time.Millisecond, Backoff: 1}
	h.write(h.srcDir, "a.csv", "x")
	h.write(archive, "a.csv", "already archived")

	var buf bytes.Buffer
	e := New(Options{
		Config: h.cfg, Direction: config.DirSend,
		NewSrc: func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst: func(context.Context) (fsys.FS, error) { return h.dst, nil },
		Logger: slog.New(slog.NewJSONHandler(&buf, nil)), Single: true,
		OnResult: func(r Result) { h.results = append(h.results, r) },
	})
	if _, err := e.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if n := strings.Count(buf.String(), "retrying transfer"); n != 0 {
		t.Errorf("%d retry records for a collision that cannot resolve itself", n)
	}
	if r := h.result("a.csv"); r.Outcome != Failed || !errors.Is(r.Err, fs.ErrExist) {
		t.Errorf("result = %+v, want a failure recognisable as an existing file", r)
	}
}
