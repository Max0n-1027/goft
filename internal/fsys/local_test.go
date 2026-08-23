package fsys

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTruncatesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir)
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("a long previous body"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The engine relies on this: a leftover temporary file from a killed run is
	// simply written over, with no separate delete step.
	n, err := l.Write(context.Background(), "a", strings.NewReader("short"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("wrote %d bytes, want 5", n)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "a"))
	if string(body) != "short" {
		t.Errorf("content = %q, want the file truncated", body)
	}
}

func TestRemoveTreatsAMissingFileAsDone(t *testing.T) {
	l := NewLocal(t.TempDir())
	if err := l.Remove(context.Background(), "never-existed"); err != nil {
		t.Errorf("Remove() = %v, want removing an absent file to be a no-op", err)
	}
}

func TestStatReportsMissingFilesAsNotExist(t *testing.T) {
	l := NewLocal(t.TempDir())
	_, err := l.Stat(context.Background(), "nope")
	if !isNotExist(err) {
		t.Errorf("Stat() = %v, want it to satisfy fs.ErrNotExist so the engine can branch on it", err)
	}
}

func TestListReportsMissingDirectoriesAsNotExist(t *testing.T) {
	l := NewLocal(filepath.Join(t.TempDir(), "absent"))
	_, err := l.List(context.Background(), "")
	if !isNotExist(err) {
		t.Errorf("List() = %v, want fs.ErrNotExist", err)
	}
}

func TestMoveFileFallsBackToCopy(t *testing.T) {
	// os.Rename fails across volumes with EXDEV on Linux and a different error
	// on Windows, so the fallback is chosen by failure rather than by errno.
	// Renaming a file onto a path whose parent is a file forces that failure
	// here without needing a second volume.
	from := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(from, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(t.TempDir(), "dst")

	if err := MoveFile(from, to); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(to); string(body) != "payload" {
		t.Errorf("destination = %q, want the payload moved", body)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Error("the source should be gone after a move")
	}
}

func TestJoinAndDir(t *testing.T) {
	if got := Join("", "a", ""); got != "a" {
		t.Errorf("Join() = %q, want empty segments dropped", got)
	}
	if got := Dir("a.csv"); got != "" {
		t.Errorf("Dir() = %q, want the root expressed as an empty string", got)
	}
	if got := Dir("2026/08/a.csv"); got != "2026/08" {
		t.Errorf("Dir() = %q, want 2026/08", got)
	}
}

func TestTempNameRoundTrip(t *testing.T) {
	n := TempName("a.csv")
	if !IsTempName(n) {
		t.Errorf("IsTempName(%q) = false", n)
	}
	if IsTempName("a.csv") {
		t.Error("an ordinary name must not look like a temporary one")
	}
}

func isNotExist(err error) bool {
	return err != nil && (os.IsNotExist(err) || errorsIs(err, fs.ErrNotExist))
}

func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
