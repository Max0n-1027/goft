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

// runAtLevel performs one cycle with the logger set to level and returns the
// records it produced.
func runAtLevel(t *testing.T, h *harness, level slog.Level) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	e := New(Options{
		Config:    h.cfg,
		Direction: config.DirSend,
		NewSrc:    func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst:    func(context.Context) (fsys.FS, error) { return h.dst, nil },
		Logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})).
			With("job", h.cfg.Name, "direction", string(config.DirSend)),
		Single: true,
	})
	if _, err := e.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		out = append(out, rec)
	}
	return out
}

func hasRecord(recs []map[string]any, key, value string) bool {
	for _, r := range recs {
		if v, ok := r[key].(string); ok && v == value {
			return true
		}
	}
	return false
}

func TestLogLevelsAreNested(t *testing.T) {
	debug := runAtLevel(t, newSeeded(t), slog.LevelDebug)
	info := runAtLevel(t, newSeeded(t), slog.LevelInfo)
	warn := runAtLevel(t, newSeeded(t), slog.LevelWarn)

	if len(debug) <= len(info) {
		t.Errorf("debug produced %d records and info %d; debug must add detail", len(debug), len(info))
	}
	if len(info) <= len(warn) {
		t.Errorf("info produced %d records and warn %d; info must add the per-file lines", len(info), len(warn))
	}

	if !hasRecord(info, "result", "success") {
		t.Error("info should carry one line per file")
	}
	if hasRecord(warn, "result", "success") {
		t.Error("warn should drop the per-file success lines")
	}
}

func TestHashesAreRecordedAtInfo(t *testing.T) {
	// The digests are the proof that a file arrived intact, so they are part of
	// the ordinary transfer record rather than a debugging extra.
	var src, dst string
	for _, r := range runAtLevel(t, newSeeded(t), slog.LevelInfo) {
		if r["result"] != "success" {
			continue
		}
		src, _ = r["hash_src"].(string)
		dst, _ = r["hash_dst"].(string)
	}
	if src == "" || dst == "" {
		t.Fatal("a successful transfer should record both hashes at info level")
	}
	if src != dst {
		t.Errorf("hash_src = %q and hash_dst = %q, want them equal for a verified transfer", src, dst)
	}
	if len(src) != 16 {
		t.Errorf("hash_src = %q, want a zero padded 16 digit hex digest", src)
	}
}

func TestTimingDetailIsDebugOnly(t *testing.T) {
	info := runAtLevel(t, newSeededAt(t, "info"), slog.LevelInfo)
	debug := runAtLevel(t, newSeededAt(t, "debug"), slog.LevelDebug)

	for _, r := range info {
		for _, key := range []string{"rate_mibs", "step"} {
			if _, ok := r[key]; ok {
				t.Errorf("%s appears at info level; it is meant for debug only", key)
			}
		}
	}

	var sawRate, sawStep bool
	for _, r := range debug {
		if _, ok := r["rate_mibs"]; ok {
			sawRate = true
		}
		if _, ok := r["step"]; ok {
			sawStep = true
		}
	}
	if !sawRate {
		t.Error("debug should record the transfer rate")
	}
	if !sawStep {
		t.Error("debug should break a transfer down into its steps")
	}
}

func TestEveryRecordCarriesJobAndDirection(t *testing.T) {
	for _, r := range runAtLevel(t, newSeeded(t), slog.LevelInfo) {
		if r["job"] != "test" {
			t.Errorf("record %v is missing the job name, which is how logs from several processes are told apart", r)
		}
		if r["direction"] != "send" {
			t.Errorf("record %v is missing the direction", r)
		}
	}
}

func TestSizeLimitQuietensDownOnLaterCycles(t *testing.T) {
	h := newSeededBig(t)

	var buf bytes.Buffer
	e := New(Options{
		Config:    h.cfg,
		Direction: config.DirSend,
		NewSrc:    func(context.Context) (fsys.FS, error) { return h.src, nil },
		NewDst:    func(context.Context) (fsys.FS, error) { return h.dst, nil },
		Logger:    slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	countAt := func(level string) int {
		n := 0
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var rec map[string]any
			if json.Unmarshal([]byte(line), &rec) == nil &&
				rec["level"] == level && rec["reason"] == "size_limit" {
				n++
			}
		}
		return n
	}

	if _, err := e.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if countAt("WARN") != 1 {
		t.Fatalf("first cycle logged %d warnings, want exactly one", countAt("WARN"))
	}

	buf.Reset()
	if _, err := e.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The file is still there and still too big. Repeating the warning every
	// poll would drown the log, so later cycles drop to debug.
	if got := countAt("WARN"); got != 0 {
		t.Errorf("second cycle logged %d warnings, want none", got)
	}
	if got := countAt("DEBUG"); got != 1 {
		t.Errorf("second cycle logged %d debug records, want the condition still recorded once", got)
	}
}

func newSeeded(t *testing.T) *harness {
	h := newHarness(t)
	h.write(h.srcDir, "a.csv", "hello")
	return h
}

// newSeededAt seeds a harness whose configured level matches the one the test
// logs at, because the default field set depends on it.
func newSeededAt(t *testing.T, level string) *harness {
	h := newSeeded(t)
	h.cfg.Log.Level = level
	return h
}

func newSeededBig(t *testing.T) *harness {
	h := newHarness(t)
	h.cfg.MaxFileSizeMB = 1
	h.write(h.srcDir, "big.dat", strings.Repeat("x", 2*1024*1024))
	return h
}

func TestConfiguredFieldsDecideWhatIsWritten(t *testing.T) {
	h := newSeeded(t)
	h.cfg.Log.Fields = []string{config.FieldSrc, config.FieldResult, config.FieldHashSrc, config.FieldHashDst}

	var transfer map[string]any
	for _, r := range runAtLevel(t, h, slog.LevelInfo) {
		if r["event"] == "transfer" {
			transfer = r
		}
	}
	if transfer == nil {
		t.Fatal("no transfer record was written")
	}

	for _, want := range []string{"src", "result", "hash_src", "hash_dst"} {
		if _, ok := transfer[want]; !ok {
			t.Errorf("field %q was requested but is missing from %v", want, transfer)
		}
	}
	// Anything not asked for stays out, however useful it might be.
	for _, unwanted := range []string{"dst", "protocol", "bytes", "duration_ms", "verify"} {
		if _, ok := transfer[unwanted]; ok {
			t.Errorf("field %q was not requested but appears in %v", unwanted, transfer)
		}
	}
	// Record identity is never up for selection.
	for _, always := range []string{"time", "level", "msg", "job", "direction", "event"} {
		if _, ok := transfer[always]; !ok {
			t.Errorf("field %q identifies the record and must always be written", always)
		}
	}
}

func TestRateCanBeAskedForAtAnyLevel(t *testing.T) {
	h := newSeeded(t)
	h.cfg.Log.Fields = []string{config.FieldResult, config.FieldRateMiBs}

	var found bool
	for _, r := range runAtLevel(t, h, slog.LevelInfo) {
		if r["event"] == "transfer" {
			_, found = r["rate_mibs"]
		}
	}
	if !found {
		// The level decides which records are written; the fields decide what
		// each one carries.
		t.Error("rate_mibs was requested explicitly and should appear at info level")
	}
}

func TestDestinationIsRecorded(t *testing.T) {
	h := newSeeded(t)

	var dst string
	for _, r := range runAtLevel(t, h, slog.LevelInfo) {
		if r["event"] == "transfer" {
			dst, _ = r["dst"].(string)
		}
	}
	if dst == "" {
		t.Fatal("a transfer record should say where the file went")
	}
	// A send lands on the remote, so the destination is the remote location.
	if !strings.Contains(dst, "a.csv") || !strings.HasPrefix(dst, "sftp://") {
		t.Errorf("dst = %q, want the remote location of the file", dst)
	}
}
