package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"goft/internal/config"
	"goft/internal/fsys"
)

// cycleRecords runs n cycles and returns every record they produced.
func cycleRecords(t *testing.T, h *harness, n int) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	e := New(Options{
		Config:    h.cfg,
		Direction: config.DirSend,
		NewSrc:    func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst:    func(context.Context) (fsys.FS, error) { return h.dst, nil },
		Logger:    slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Single:    true,
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

	records := cycleRecords(t, h, 1)
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
	for _, r := range cycleRecords(t, h, 3) {
		if id, ok := r["cycle_id"].(string); ok {
			ids[id] = true
		}
	}
	if len(ids) != 3 {
		t.Errorf("three cycles produced %d ids, want one each", len(ids))
	}
}

func TestCycleIDLooksLikeAUUID(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "hello")

	id, _ := cycleRecords(t, h, 1)[0]["cycle_id"].(string)
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Errorf("cycle_id = %q, want a 36 character UUID", id)
	}
	if id[14] != '4' {
		t.Errorf("cycle_id = %q, want version 4", id)
	}
}

func TestAFailedCycleStillCarriesItsID(t *testing.T) {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "hello")
	h.dst.FailOp(fsys.OpWrite, errStr("disk full"))

	records := cycleRecords(t, h, 1)
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
