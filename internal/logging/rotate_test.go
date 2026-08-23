package logging

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"goft/internal/config"
)

func logNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestDailyRotationStartsANewFileEachDay(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

	w := newRotatingWriter(config.Log{
		Path:      filepath.Join(dir, "job.log"),
		Rotation:  config.RotationDaily,
		MaxSizeMB: 100,
	}, func() time.Time { return now })
	defer w.Close()

	if _, err := w.Write([]byte("day one\n")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if _, err := w.Write([]byte("day two\n")); err != nil {
		t.Fatal(err)
	}

	want := []string{"job-2026-08-20.log", "job-2026-08-21.log"}
	if got := logNames(t, dir); !equalStrings(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

func TestMonthlyRotationStartsANewFileEachMonth(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC)

	w := newRotatingWriter(config.Log{
		Path:     filepath.Join(dir, "job.log"),
		Rotation: config.RotationMonthly,
	}, func() time.Time { return now })
	defer w.Close()

	w.Write([]byte("august\n"))
	now = now.Add(2 * time.Hour) // into September
	w.Write([]byte("september\n"))

	want := []string{"job-2026-08.log", "job-2026-09.log"}
	if got := logNames(t, dir); !equalStrings(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

func TestDailyRotationPrunesOldGenerations(t *testing.T) {
	// lumberjack only ever cleans up backups of its own current file name.
	// Because date rotation changes that name every day, yesterday's files
	// belong to no one, and would pile up forever without this.
	dir := t.TempDir()
	now := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

	w := newRotatingWriter(config.Log{
		Path:       filepath.Join(dir, "job.log"),
		Rotation:   config.RotationDaily,
		MaxBackups: 2,
	}, func() time.Time { return now })
	defer w.Close()

	for i := 0; i < 5; i++ {
		if _, err := w.Write([]byte("entry\n")); err != nil {
			t.Fatal(err)
		}
		now = now.Add(24 * time.Hour)
	}

	got := logNames(t, dir)
	if len(got) != 3 {
		t.Fatalf("files = %v, want the active file plus 2 kept generations", got)
	}
	want := []string{"job-2026-08-22.log", "job-2026-08-23.log", "job-2026-08-24.log"}
	if !equalStrings(got, want) {
		t.Errorf("files = %v, want the newest kept: %v", got, want)
	}
}

func TestDailyRotationPrunesByAge(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

	// Seed a file that is far older than the retention window. Its modification
	// time is what the age check looks at.
	stale := filepath.Join(dir, "job-2026-01-01.log")
	if err := os.WriteFile(stale, []byte("ancient\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.AddDate(0, 0, -200)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	w := newRotatingWriter(config.Log{
		Path:       filepath.Join(dir, "job.log"),
		Rotation:   config.RotationDaily,
		MaxAgeDays: 30,
	}, func() time.Time { return now })
	defer w.Close()
	w.Write([]byte("today\n"))

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a file older than max_age_days should have been removed")
	}
}

func TestSizeRotationLeavesTheNameAlone(t *testing.T) {
	dir := t.TempDir()
	w := newRotatingWriter(config.Log{
		Path:      filepath.Join(dir, "job.log"),
		Rotation:  config.RotationSize,
		MaxSizeMB: 1,
	}, time.Now)
	defer w.Close()

	w.Write([]byte("hello\n"))
	if got := logNames(t, dir); len(got) != 1 || got[0] != "job.log" {
		t.Errorf("files = %v, want just job.log", got)
	}
}

func TestRotationDoesNotTouchUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other-2026-01-01.log")
	if err := os.WriteFile(other, []byte("not ours\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -400)
	os.Chtimes(other, old, old)

	now := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	w := newRotatingWriter(config.Log{
		Path:       filepath.Join(dir, "job.log"),
		Rotation:   config.RotationDaily,
		MaxBackups: 1,
		MaxAgeDays: 1,
	}, func() time.Time { return now })
	defer w.Close()
	w.Write([]byte("x\n"))
	now = now.Add(48 * time.Hour)
	w.Write([]byte("y\n"))

	if _, err := os.Stat(other); err != nil {
		t.Error("a log belonging to another job must never be pruned")
	}
}

func TestConcurrentWritesDoNotInterleave(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	w := newRotatingWriter(config.Log{
		Path:     filepath.Join(dir, "job.log"),
		Rotation: config.RotationDaily,
	}, func() time.Time { return now })
	defer w.Close()

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				w.Write([]byte(strings.Repeat("a", 64) + "\n"))
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}

	body, err := os.ReadFile(filepath.Join(dir, "job-"+now.Format(layoutDaily)+".log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if len(line) != 64 {
			t.Fatalf("found a line of length %d; writes must not interleave", len(line))
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
