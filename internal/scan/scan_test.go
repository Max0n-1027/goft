package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"goft/internal/fsys"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func paths(files []File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

func TestScanAppliesIncludeThenExclude(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.csv", "a")
	write(t, dir, "b.csv", "b")
	write(t, dir, "b.csv.part", "partial")
	write(t, dir, "notes.txt", "x")

	got, err := Scan(context.Background(), fsys.NewLocal(dir), false,
		NewFilter([]string{"*.csv"}, []string{"b.*"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.csv"}; !equal(paths(got), want) {
		t.Errorf("Scan() = %v, want %v", paths(got), want)
	}
}

func TestScanIgnoresSubdirectoriesUnlessRecursive(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "top.csv", "1")
	write(t, dir, "sub/deep.csv", "2")

	flat, err := Scan(context.Background(), fsys.NewLocal(dir), false, NewFilter(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"top.csv"}; !equal(paths(flat), want) {
		t.Errorf("non-recursive Scan() = %v, want %v", paths(flat), want)
	}

	deep, err := Scan(context.Background(), fsys.NewLocal(dir), true, NewFilter(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"top.csv", "sub/deep.csv"}; !sameSet(paths(deep), want) {
		t.Errorf("recursive Scan() = %v, want %v", paths(deep), want)
	}
}

func TestScanPrunesExcludedDirectories(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "keep/a.csv", "1")
	write(t, dir, ".git/objects/b.csv", "2")

	got, err := Scan(context.Background(), fsys.NewLocal(dir), true, NewFilter(nil, []string{".*"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"keep/a.csv"}; !equal(paths(got), want) {
		t.Errorf("Scan() = %v, want %v: an excluded directory should be pruned whole", paths(got), want)
	}
}

func TestScanNeverPicksUpItsOwnTempFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.csv", "a")
	write(t, dir, "b.csv"+fsys.TempSuffix, "in flight")

	// A user who overrides exclude must not thereby start transferring goft's
	// own in-flight files, so the suffix is excluded unconditionally.
	got, err := Scan(context.Background(), fsys.NewLocal(dir), false,
		NewFilter([]string{"*"}, []string{"nothing-matches"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.csv"}; !equal(paths(got), want) {
		t.Errorf("Scan() = %v, want %v", paths(got), want)
	}
}

func TestScanSkipsIrregularEntries(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "real.csv", "1")
	if err := os.Symlink(filepath.Join(dir, "real.csv"), filepath.Join(dir, "link.csv")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := Scan(context.Background(), fsys.NewLocal(dir), false, NewFilter(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"real.csv"}; !equal(paths(got), want) {
		t.Errorf("Scan() = %v, want %v: only regular files are transferred", paths(got), want)
	}
}

func TestStabilizerWaitsForFilesToSettle(t *testing.T) {
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	s := NewStabilizer(3*time.Second, func() time.Time { return now })

	growing := []File{{Path: "a", Size: 10, ModTime: now}}
	if got := s.Observe(growing); len(got) != 0 {
		t.Fatalf("a file seen for the first time cannot be settled yet, got %v", paths(got))
	}

	// Still growing two seconds later.
	now = now.Add(2 * time.Second)
	growing = []File{{Path: "a", Size: 20, ModTime: now}}
	if got := s.Observe(growing); len(got) != 0 {
		t.Fatalf("a file that changed must restart the wait, got %v", paths(got))
	}

	// Unchanged, but not for long enough yet.
	now = now.Add(2 * time.Second)
	steady := []File{{Path: "a", Size: 20, ModTime: now.Add(-2 * time.Second)}}
	if got := s.Observe(steady); len(got) != 0 {
		t.Fatalf("only 2s of the 3s wait has passed, got %v", paths(got))
	}

	now = now.Add(2 * time.Second)
	if got := s.Observe(steady); len(got) != 1 {
		t.Fatalf("the file has been unchanged for 4s and should be settled, got %v", paths(got))
	}
}

func TestStabilizerAccumulatesAcrossShortPolls(t *testing.T) {
	// A poll interval shorter than the settle duration must not shorten the
	// wait: the timestamp of the last change is what counts, not the gap
	// between two observations.
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	s := NewStabilizer(10*time.Second, func() time.Time { return now })

	f := []File{{Path: "a", Size: 1, ModTime: now}}
	s.Observe(f)
	for i := 0; i < 4; i++ {
		now = now.Add(time.Second)
		if got := s.Observe(f); len(got) != 0 {
			t.Fatalf("settled after only %ds of a 10s wait", i+1)
		}
	}
	now = now.Add(6 * time.Second)
	if got := s.Observe(f); len(got) != 1 {
		t.Fatal("want the file settled once 10s have passed")
	}
}

func TestStabilizerReportsWhatChanged(t *testing.T) {
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	s := NewStabilizer(0, func() time.Time { return now })

	f := []File{{Path: "big.dat", Size: 99, ModTime: now}}
	s.Observe(f)
	if !s.ChangedInLastCycle("big.dat") {
		t.Error("a newly seen file counts as changed, so a size limit is reported loudly once")
	}

	now = now.Add(time.Minute)
	s.Observe(f)
	if s.ChangedInLastCycle("big.dat") {
		t.Error("an unchanged file must not count as changed, so the report quietens down")
	}
}

func TestStabilizerForgetsVanishedFiles(t *testing.T) {
	now := time.Now()
	s := NewStabilizer(0, func() time.Time { return now })
	s.Observe([]File{{Path: "gone", Size: 1}})
	s.Observe(nil)
	if s.ChangedInLastCycle("gone") {
		t.Error("a path that left the listing should be forgotten")
	}
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}
