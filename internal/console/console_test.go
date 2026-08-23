package console

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/engine"
	"goft/internal/scan"
)

func newTestConsole(buf *bytes.Buffer, level slog.Level, dir config.Direction) *Console {
	return New(Options{
		Writer:    buf,
		Level:     level,
		Job:       "invoice",
		Direction: dir,
		Local:     "/data/out",
		Remote:    "sftp://host/upload",
	})
}

func TestHeaderShowsTheDirection(t *testing.T) {
	for _, tc := range []struct {
		dir  config.Direction
		want string
	}{
		{config.DirSend, "/data/out -> sftp://host/upload"},
		{config.DirRecv, "/data/out <- sftp://host/upload"},
	} {
		var buf bytes.Buffer
		c := newTestConsole(&buf, slog.LevelInfo, tc.dir)
		c.Plan(engine.Plan{Files: []scan.File{{Path: "a.csv", Size: 10}}, Bytes: 10})

		if !strings.Contains(buf.String(), tc.want) {
			t.Errorf("header = %q, want it to contain %q", buf.String(), tc.want)
		}
	}
}

func TestQuietCycleProducesNoOutput(t *testing.T) {
	var buf bytes.Buffer
	c := newTestConsole(&buf, slog.LevelInfo, config.DirSend)

	// A polling cycle that found nothing should not print anything at all.
	c.Plan(engine.Plan{})
	c.Summary(engine.Summary{})

	if buf.Len() != 0 {
		t.Errorf("output = %q, want nothing for an empty cycle", buf.String())
	}
}

func TestResultLinesFollowTheLevel(t *testing.T) {
	success := engine.Result{Index: 1, Total: 1, Path: "a.csv", Outcome: engine.Success}
	oversized := engine.Result{Index: 1, Total: 1, Path: "big.dat",
		Outcome: engine.Skipped, Reason: engine.ReasonSizeLimit}

	var atInfo, atWarn bytes.Buffer
	newTestConsole(&atInfo, slog.LevelInfo, config.DirSend).Result(success)
	newTestConsole(&atWarn, slog.LevelWarn, config.DirSend).Result(success)

	if atInfo.Len() == 0 {
		t.Error("info should print a line per file")
	}
	if atWarn.Len() != 0 {
		t.Errorf("warn printed %q, want per-file successes suppressed", atWarn.String())
	}

	var warnBuf bytes.Buffer
	newTestConsole(&warnBuf, slog.LevelWarn, config.DirSend).Result(oversized)
	if !strings.Contains(warnBuf.String(), "size_limit") {
		t.Errorf("output = %q, want the size limit still reported at warn", warnBuf.String())
	}
}

func TestFailureLineCarriesTheReason(t *testing.T) {
	var buf bytes.Buffer
	c := newTestConsole(&buf, slog.LevelInfo, config.DirSend)
	c.Result(engine.Result{Index: 1, Total: 1, Path: "a.csv",
		Outcome: engine.Failed, Err: errString("hash mismatch")})

	out := buf.String()
	if !strings.Contains(out, "failed") || !strings.Contains(out, "hash mismatch") {
		t.Errorf("output = %q, want the failure and its cause", out)
	}
}

func TestNoColourWhenNotATerminal(t *testing.T) {
	var buf bytes.Buffer
	c := New(Options{Writer: &buf, Level: slog.LevelInfo, Job: "j", Direction: config.DirSend})
	c.Result(engine.Result{Index: 1, Total: 1, Path: "a.csv", Outcome: engine.Success})

	if strings.Contains(buf.String(), "\033[") {
		t.Errorf("output = %q, want no escape sequences when piped", buf.String())
	}
}

func TestColourKeepsColumnsAligned(t *testing.T) {
	var plain, coloured bytes.Buffer
	New(Options{Writer: &plain, Level: slog.LevelInfo}).
		Result(engine.Result{Index: 1, Total: 1, Path: "a.csv", Outcome: engine.Success, Elapsed: time.Second})
	New(Options{Writer: &coloured, Level: slog.LevelInfo, Color: true}).
		Result(engine.Result{Index: 1, Total: 1, Path: "a.csv", Outcome: engine.Success, Elapsed: time.Second})

	stripped := strings.NewReplacer("\033[32m", "", "\033[0m", "").Replace(coloured.String())
	if stripped != plain.String() {
		t.Errorf("colour changed the layout:\n plain = %q\n stripped = %q", plain.String(), stripped)
	}
}

func TestDryRunUsesItsOwnFormat(t *testing.T) {
	var buf bytes.Buffer
	c := newTestConsole(&buf, slog.LevelInfo, config.DirSend)
	c.Plan(engine.Plan{DryRun: true, Files: []scan.File{{Path: "a.csv", Size: 2048}}, Bytes: 2048})
	c.Summary(engine.Summary{Total: 1, Bytes: 2048, PlannedOnly: true})

	out := buf.String()
	// A dry run never looks at the destination, so it cannot say ok or skipped.
	for _, unwanted := range []string{"ok", "skipped", "failed"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("dry run output contains %q, which it cannot know:\n%s", unwanted, out)
		}
	}
	for _, want := range []string{"dry-run", "a.csv", "2.0 KiB", "nothing was sent"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output is missing %q:\n%s", want, out)
		}
	}
}

func TestSummaryCountsEveryOutcome(t *testing.T) {
	var buf bytes.Buffer
	c := newTestConsole(&buf, slog.LevelInfo, config.DirSend)
	c.Plan(engine.Plan{Files: []scan.File{{Path: "a"}}})
	c.Summary(engine.Summary{Total: 12, Succeeded: 9, Skipped: 2, Failed: 1,
		Bytes: 22020096, Elapsed: 12400 * time.Millisecond})

	out := buf.String()
	for _, want := range []string{"12 files", "9 transferred", "21.0 MiB", "2 skipped", "1 failed", "12.4s"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary is missing %q:\n%s", want, out)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KiB"},
		{22020096, "21.0 MiB"},
		{2147483648, "2.0 GiB"},
	} {
		if got := humanBytes(tc.in); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestWatchingRunStaysSilentWhenNothingMoved(t *testing.T) {
	var buf bytes.Buffer
	c := New(Options{Writer: &buf, Level: slog.LevelInfo, Job: "j",
		Direction: config.DirSend, Buffered: true})

	// A poll where every file was already on the other side. Reporting it
	// every few seconds would bury the cycles that did something.
	c.Plan(engine.Plan{Files: []scan.File{{Path: "a.csv", Size: 4}}, Bytes: 4})
	c.Result(engine.Result{Index: 1, Total: 1, Path: "a.csv",
		Outcome: engine.Skipped, Reason: engine.ReasonIdentical})
	c.Summary(engine.Summary{Total: 1, Skipped: 1})

	if buf.Len() != 0 {
		t.Errorf("output = %q, want a cycle that moved nothing to stay quiet", buf.String())
	}
}

func TestWatchingRunReportsCyclesThatDidSomething(t *testing.T) {
	var buf bytes.Buffer
	c := New(Options{Writer: &buf, Level: slog.LevelInfo, Job: "j",
		Direction: config.DirSend, Buffered: true})

	c.Plan(engine.Plan{Files: []scan.File{{Path: "a.csv", Size: 4}}, Bytes: 4})
	c.Result(engine.Result{Index: 1, Total: 1, Path: "a.csv", Outcome: engine.Success, Bytes: 4})
	c.Summary(engine.Summary{Total: 1, Succeeded: 1, Bytes: 4})

	out := buf.String()
	if !strings.Contains(out, "a.csv") || !strings.Contains(out, "1 transferred") {
		t.Errorf("output = %q, want the whole cycle reported once it did something", out)
	}
}

func TestWatchingRunReportsFailuresAndWarnings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result engine.Result
	}{
		{"failure", engine.Result{Index: 1, Total: 1, Path: "a.csv",
			Outcome: engine.Failed, Err: errString("boom")}},
		{"new size limit", engine.Result{Index: 1, Total: 1, Path: "big.dat",
			Outcome: engine.Skipped, Reason: engine.ReasonSizeLimit}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			c := New(Options{Writer: &buf, Level: slog.LevelInfo, Job: "j",
				Direction: config.DirSend, Buffered: true})
			c.Plan(engine.Plan{Files: []scan.File{{Path: tc.result.Path}}})
			c.Result(tc.result)
			c.Summary(engine.Summary{Total: 1})

			if buf.Len() == 0 {
				t.Error("something that needs attention must not be swallowed")
			}
		})
	}
}

func TestOneShotRunPrintsAsItGoes(t *testing.T) {
	var buf bytes.Buffer
	c := New(Options{Writer: &buf, Level: slog.LevelInfo, Job: "j", Direction: config.DirSend})

	c.Plan(engine.Plan{Files: []scan.File{{Path: "a.csv", Size: 4}}, Bytes: 4})
	if buf.Len() == 0 {
		t.Error("a one-shot run should show progress immediately, not at the end")
	}
}

func TestResultLineMentionsRetries(t *testing.T) {
	var buf bytes.Buffer
	c := newTestConsole(&buf, slog.LevelInfo, config.DirSend)
	c.Result(engine.Result{Index: 1, Total: 1, Path: "a.csv", Outcome: engine.Success, Attempts: 3})

	// A file that only made it on the third go is worth noticing, even though
	// the outcome is a plain success.
	if !strings.Contains(buf.String(), "attempt 3") {
		t.Errorf("output = %q, want it to mention the retries", buf.String())
	}
}

func TestCycleErrorIsAlwaysShown(t *testing.T) {
	var buf bytes.Buffer
	c := New(Options{Writer: &buf, Level: slog.LevelInfo, Job: "j",
		Direction: config.DirSend, Buffered: true})

	// A watching run failing every poll must not look like a quiet one.
	c.Plan(engine.Plan{Files: []scan.File{{Path: "a.csv"}}})
	c.CycleError(errString("dial tcp: connection refused"))

	if !strings.Contains(buf.String(), "connection refused") {
		t.Errorf("output = %q, want the failure reported", buf.String())
	}
}

func TestCycleErrorShowsWhatTheCycleHadFound(t *testing.T) {
	var buf bytes.Buffer
	c := New(Options{Writer: &buf, Level: slog.LevelInfo, Job: "j",
		Direction: config.DirSend, Local: "/out", Remote: "sftp://host/in", Buffered: true})

	// The header had already been written when the destination turned out to
	// be unreachable. Both halves of that story are useful.
	c.Plan(engine.Plan{Files: []scan.File{{Path: "a.csv"}, {Path: "b.csv"}}, Bytes: 20})
	c.CycleError(errString("dial tcp: connection refused"))

	out := buf.String()
	if !strings.Contains(out, "2 files") {
		t.Errorf("output = %q, want it to keep what the cycle had found", out)
	}
	if !strings.Contains(out, "connection refused") {
		t.Errorf("output = %q, want the failure", out)
	}
}
