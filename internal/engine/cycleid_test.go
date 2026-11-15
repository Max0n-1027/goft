package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"goft/internal/fsys"
)

// cycleRecords runs n cycles and returns every record they produced. now is
// the engine's clock, or nil for the real one.
func cycleRecords(t *testing.T, h *harness, n int, now func() time.Time) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	e := New(Options{
		Job:    h.cfg,
		NewSrc: func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst: func(context.Context) (fsys.FS, error) { return h.dst, nil },
		Logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Single: true,
		Now:    now,
	})
	for i := 0; i < n; i++ {
		if _, err := e.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("not JSON: %q", line)
		}
		out = append(out, r)
	}
	return out
}

func TestEveryRecordInACycleSharesOneID(t *testing.T) {
	h := newHarness(t)
	for _, n := range []string{"a", "b", "c"} {
		h.write(h.srcDir, n+".csv", n)
	}

	records := cycleRecords(t, h, 1, nil)
	if len(records) < 4 {
		t.Fatalf("only %d records; expected a scan, three transfers and a summary", len(records))
	}

	ids := map[string]bool{}
	for _, r := range records {
		id, ok := r["cycle_id"].(string)
		if !ok {
			t.Errorf("record %v carries no cycle_id", r["msg"])
			continue
		}
		ids[id] = true
	}
	if len(ids) != 1 {
		t.Errorf("one cycle produced %d different ids, want them all the same", len(ids))
	}
}

func TestEachCycleGetsItsOwnID(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "hello")

	// Three passes over the same directory. Without a per-cycle id, the records
	// of a watcher running for days would be one undifferentiated stream.
	ids := map[string]bool{}
	for _, r := range cycleRecords(t, h, 3, nil) {
		if id, ok := r["cycle_id"].(string); ok {
			ids[id] = true
		}
	}
	if len(ids) != 3 {
		t.Errorf("three cycles produced %d ids, want one each", len(ids))
	}
}

func TestCycleIDIsAULIDOfWhenTheCycleStarted(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "hello")
	start := time.Date(2026, 11, 15, 9, 30, 12, 345678901, time.Local)

	raw, _ := cycleRecords(t, h, 1, func() time.Time { return start })[0]["cycle_id"].(string)
	id, err := ulid.ParseStrict(raw)
	if err != nil {
		t.Fatalf("cycle_id = %q, want a ULID: %v", raw, err)
	}
	if raw != id.String() {
		t.Errorf("cycle_id = %q, want it written as a ULID is, %q", raw, id.String())
	}
	if want := start.Truncate(time.Millisecond); !id.Timestamp().Equal(want) {
		t.Errorf("cycle_id %s says the cycle started at %v, want %v", raw, id.Timestamp(), want)
	}
}

// TestCycleIDsSortInTheOrderTheCyclesStarted holds even for cycles started in
// the same millisecond, which a clock that stands still makes every cycle.
func TestCycleIDsSortInTheOrderTheCyclesStarted(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "hello")
	still := time.Now()

	for name, now := range map[string]func() time.Time{
		"real clock":      nil,
		"one millisecond": func() time.Time { return still },
	} {
		var ids []string
		for _, r := range cycleRecords(t, h, 5, now) {
			if id, _ := r["cycle_id"].(string); len(ids) == 0 || ids[len(ids)-1] != id {
				ids = append(ids, id)
			}
		}
		if len(ids) != 5 {
			t.Fatalf("%s: five cycles produced %d ids", name, len(ids))
		}
		if !slices.IsSorted(ids) || len(slices.Compact(slices.Clone(ids))) != 5 {
			t.Errorf("%s: cycle ids in the order the cycles ran: %v, want them strictly increasing", name, ids)
		}
	}
}

func TestAFailedCycleStillCarriesItsID(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "hello")
	h.dst.FailOp(fsys.OpWrite, errStr("disk full"))

	records := cycleRecords(t, h, 1, nil)
	var failure map[string]any
	for _, r := range records {
		if r["result"] == "failed" {
			failure = r
		}
	}
	if failure == nil {
		t.Fatal("no failure was recorded")
	}
	// A failure is exactly the record someone will want to trace back to the
	// rest of its cycle.
	if _, ok := failure["cycle_id"].(string); !ok {
		t.Error("the failure record carries no cycle_id")
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }
