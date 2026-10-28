package console

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/engine"
	"goft/internal/i18n"
	"goft/internal/scan"
)

func newJapaneseConsole(buf *bytes.Buffer, level slog.Level) *Console {
	return New(Options{
		Writer:    buf,
		Printer:   i18n.Japanese,
		Level:     level,
		Job:       "invoice",
		Direction: config.DirSend,
		Local:     "/data/out",
		Remote:    "sftp://host/upload",
	})
}

func TestTheConsoleSpeaksJapanese(t *testing.T) {
	var buf bytes.Buffer
	c := newJapaneseConsole(&buf, slog.LevelInfo)

	c.Plan(engine.Plan{Files: []scan.File{{Path: "a.csv", Size: 10}, {Path: "b.csv", Size: 20}}, Bytes: 30})
	c.Result(engine.Result{Index: 1, Total: 3, Path: "a.csv", Bytes: 10, Outcome: engine.Success, Attempts: 2})
	c.Result(engine.Result{Index: 2, Total: 3, Path: "b.csv", Bytes: 20, Outcome: engine.Skipped, Reason: engine.ReasonAlreadyExists})
	c.Result(engine.Result{Index: 3, Total: 3, Path: "c.csv", Outcome: engine.Failed,
		Err: i18n.Errorf("size mismatch: sent %d bytes, destination has %d", 10, 9)})
	c.Summary(engine.Summary{Total: 3, Succeeded: 1, Skipped: 1, Failed: 1, Bytes: 10, Elapsed: 1500 * time.Millisecond})

	out := buf.String()
	for _, want := range []string{
		"goft invoice  /data/out -> sftp://host/upload  （2 件、30 B）",
		"完了      ",
		"（2 回目）",
		"スキップ  ",
		"転送先に既にある",
		"失敗      ",
		"サイズが一致しません: 送ったのは 10 バイト、転送先は 9 バイト",
		"3 件: 転送 1（10 B）、スキップ 1、失敗 1  1.5 秒",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestJapaneseStatusesKeepTheColumnsAligned(t *testing.T) {
	var buf bytes.Buffer
	c := newJapaneseConsole(&buf, slog.LevelInfo)
	for i, outcome := range []engine.Outcome{engine.Success, engine.Skipped, engine.Failed} {
		c.Result(engine.Result{Index: i + 1, Total: 3, Path: "a.csv", Outcome: outcome})
	}

	// Whatever the status, the elapsed time starts in the same column.
	var col []int
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		i := strings.Index(line, "0.0s")
		col = append(col, displayWidth(line[:i]))
	}
	if col[0] != col[1] || col[1] != col[2] {
		t.Errorf("the time starts at columns %v, want one column:\n%s", col, buf.String())
	}
}

func TestJapaneseDryRunInterruptAndCycleError(t *testing.T) {
	var buf bytes.Buffer
	c := newJapaneseConsole(&buf, slog.LevelInfo)
	c.Plan(engine.Plan{DryRun: true, Files: []scan.File{{Path: "a.csv", Size: 10}}, Bytes: 10})
	c.Summary(engine.Summary{PlannedOnly: true, Total: 1, Bytes: 10})
	c.Summary(engine.Summary{Interrupted: true, Total: 1, NotStarted: 1, Elapsed: time.Second})
	c.CycleError(i18n.Errorf("dial %s: %w", "host:22", errString("connection refused")))

	out := buf.String()
	for _, want := range []string{
		"（ドライラン）",
		"1 件、10 B を転送する予定です（何も送っていません）",
		"未着手 1  1.0 秒で中断",
		"goft invoice  host:22 に接続できません: connection refused",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestAnUnknownReasonIsShownAsItIs(t *testing.T) {
	var buf bytes.Buffer
	c := newJapaneseConsole(&buf, slog.LevelInfo)
	c.Result(engine.Result{Index: 1, Total: 1, Path: "a.csv", Outcome: engine.Skipped, Reason: "something_new"})
	if !strings.Contains(buf.String(), "something_new") {
		t.Errorf("output = %q, want the reason shown as the engine gave it", buf.String())
	}
}
