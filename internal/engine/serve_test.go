package engine

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/fsys"
)

func (h *harness) serve(ctx context.Context) error {
	e := New(Options{
		Config:    h.cfg,
		Direction: config.DirSend,
		NewSrc:    func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst:    func(context.Context) (fsys.FS, error) { return h.dst, nil },
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	return e.Serve(ctx)
}

func TestServeStopsWhenCancelled(t *testing.T) {
	h := newHarness(t)
	h.cfg.PollInterval = time.Hour // long enough that only cancellation can end it

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.serve(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve() = %v, want a clean stop on cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after the context was cancelled")
	}
}

func TestServeTransfersOnceFilesHaveSettled(t *testing.T) {
	h := newHarness(t)
	h.cfg.PollInterval = 10 * time.Millisecond
	h.cfg.StableDuration = 50 * time.Millisecond
	h.write(h.srcDir, "a.csv", "hello")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.serve(ctx)
	}()

	deadline := time.Now().Add(time.Second)
	for !h.exists(h.dstDir, "a.csv") {
		if time.Now().After(deadline) {
			t.Fatal("the file was never transferred")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if got := h.read(h.dstDir, "a.csv"); got != "hello" {
		t.Errorf("destination = %q, want hello", got)
	}
}

func TestServeKeepsGoingAfterAFailedCycle(t *testing.T) {
	h := newHarness(t)
	h.cfg.PollInterval = 10 * time.Millisecond
	// A source that cannot be listed fails the whole cycle. A daemon must log
	// that and try again rather than exiting.
	h.src = fsys.NewErrFS(fsys.NewLocal(h.srcDir + "-absent"))

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- h.serve(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve() = %v, want it to survive failing cycles and stop cleanly", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
	if n := h.src.Count(fsys.OpList); n < 2 {
		t.Errorf("listed %d times, want the daemon to have retried", n)
	}
}
