package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/fsys"
)

// holdWrite makes the destination's first Write wait until released, and says
// when it has started, so that a test can ask the run to stop in the middle of
// a transfer.
type holdWrite struct {
	fsys.FS
	started chan struct{}
	release chan struct{}
	first   bool
}

func (h *holdWrite) Write(ctx context.Context, name string, r io.Reader) (int64, error) {
	if !h.first {
		h.first = true
		close(h.started)
		<-h.release
	}
	return h.FS.Write(ctx, name, r)
}

func TestStoppingFinishesTheFileUnderWayAndStartsNoOther(t *testing.T) {
	h := newHarness(t)
	h.cfg.Workers = 1
	for _, name := range []string{"a.csv", "b.csv", "c.csv"} {
		h.write(h.srcDir, name, "id\n")
	}
	dst := &holdWrite{FS: h.dst, started: make(chan struct{}), release: make(chan struct{})}

	var logBuf bytes.Buffer
	var summary Summary
	e := New(Options{
		Config: h.cfg, Direction: config.DirSend,
		NewSrc:    func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst:    func(context.Context) (fsys.FS, error) { return dst, nil },
		Logger:    slog.New(slog.NewJSONHandler(&logBuf, nil)),
		Single:    true,
		OnResult:  func(r Result) { h.results = append(h.results, r) },
		OnSummary: func(s Summary) { summary = s },
	})

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := e.RunOnce(ctx)
		done <- err
	}()

	<-dst.started
	stop()
	close(dst.release)

	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the run did not stop")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("RunOnce = %v, want it to report the stop", err)
	}

	// The file under way when the stop came is finished, not abandoned half
	// written; nothing after it is started.
	if r := h.result("a.csv"); r.Outcome != Success {
		t.Errorf("a.csv: %+v, want the file under way finished", r)
	}
	if !h.exists(h.dstDir, "a.csv") {
		t.Error("a.csv should have arrived")
	}
	for _, name := range []string{"b.csv", "c.csv"} {
		if h.exists(h.dstDir, name) {
			t.Errorf("%s was started after the stop", name)
		}
	}

	// What the cycle got through is still accounted for.
	if !summary.Interrupted || summary.Succeeded != 1 || summary.NotStarted != 2 {
		t.Errorf("summary = %+v, want one sent and two not started, marked interrupted", summary)
	}
	if !strings.Contains(logBuf.String(), `"msg":"cycle interrupted"`) {
		t.Errorf("the log should record the interrupted cycle:\n%s", logBuf.String())
	}
}
