//go:build windows

// Windows only tests for the local file system. Their POSIX counterparts,
// where there is one, are in pure_test.go and local_test.go and are marked
// with requirePOSIX.
package fsys

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The bug this guards: a colon makes Windows write into an NTFS alternate data
// stream, and every check goft performs afterwards passes — Stat reports the
// right size and reading the file back returns the right bytes — while the
// directory holds nothing but an empty file named up to the colon.
func TestWriteRefusesAlternateDataStreamNames(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir)

	_, err := l.Write(context.Background(), "2026:01.csv", strings.NewReader("payload"))
	if err == nil {
		t.Fatal("a name containing a colon was accepted")
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("%v does not wrap fs.ErrInvalid, so the engine would retry it", err)
	}
	// Nothing may be left behind, least of all the empty "2026" that the
	// stream would have hung off.
	if ents, err := os.ReadDir(dir); err != nil || len(ents) != 0 {
		t.Errorf("the directory holds %v after a refused write", names(ents))
	}
}

// A trailing dot or space survives the temporary name — ".goft.tmp" hides the
// end of it — so it is the rename onto the final name that has to catch it.
func TestRenameRefusesNamesWindowsWouldTruncate(t *testing.T) {
	for _, name := range []string{"report.csv.", "report.csv "} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			l := NewLocal(dir)
			ctx := context.Background()
			tmp := name + TempSuffix

			if _, err := l.Write(ctx, tmp, strings.NewReader("payload")); err != nil {
				t.Fatalf("the temporary name should be storable: %v", err)
			}
			err := l.Rename(ctx, tmp, name)
			if err == nil {
				t.Fatalf("renaming onto %q was accepted, and Windows would have dropped the end of it", name)
			}
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("%v does not wrap fs.ErrInvalid", err)
			}
			// A .goft.tmp file is all that may be left, which is the state the
			// next cycle overwrites. What must not exist is a file under the
			// name Windows would have truncated it to.
			if _, err := os.Stat(filepath.Join(dir, strings.TrimRight(name, ". "))); err == nil {
				t.Errorf("a file appeared under the truncated name %q", strings.TrimRight(name, ". "))
			}
		})
	}
}

// Go writes these through the extended-length path syntax, so they are created
// and read back happily, but ordinary Win32 path resolution cannot reach them
// again: cmd cannot even list the directory afterwards.
func TestWriteRefusesReservedDeviceNames(t *testing.T) {
	for _, name := range []string{"con", "nul.csv", "AUX", "com1.txt", "lpt9"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := NewLocal(dir).Write(context.Background(), name, strings.NewReader("payload")); err == nil {
				t.Errorf("the reserved device name %q was accepted", name)
			}
		})
	}
}

// Directories go through the same rules, since recursive: true builds them from
// remote names too.
func TestMkdirAllRefusesUnstorableNames(t *testing.T) {
	dir := t.TempDir()
	if err := NewLocal(dir).MkdirAll(context.Background(), "2026:01/reports"); err == nil {
		t.Error("a directory name containing a colon was accepted")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("the directory holds %v after a refused mkdir", names(ents))
	}
}

// The names that a POSIX server produces and Windows can hold have to keep
// working, or the check would be worse than the bug.
func TestOrdinaryNamesAreStillWritten(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir)
	ctx := context.Background()

	for _, name := range []string{"report.csv", "請求書_2026年01月.csv", "a file with spaces.csv", "no-extension"} {
		if _, err := l.Write(ctx, name+TempSuffix, strings.NewReader("payload")); err != nil {
			t.Fatalf("Write(%q): %v", name, err)
		}
		if err := l.Rename(ctx, name+TempSuffix, name); err != nil {
			t.Fatalf("Rename(%q): %v", name, err)
		}
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != "payload" {
			t.Errorf("%q = %q, %v", name, b, err)
		}
	}
}

// A file another process holds open with no sharing, which is how Windows
// applications keep a file they are still writing. The message is the only
// thing identifying it, so this is what keeps that string honest.
func TestSharingViolationIsRecognisedHere(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "locked.csv")
	if err := os.WriteFile(p, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := lockExclusive(t, p)
	defer syscall.CloseHandle(h)

	_, err := os.Open(p)
	if err == nil {
		t.Fatal("the file was supposed to be locked")
	}
	if !isSharingViolation(err) {
		t.Errorf("isSharingViolation(%v) = false: a locked file would not be retried", err)
	}
}

// Remove waits for a lock to clear rather than reporting a failure that a
// moment's patience would have avoided.
func TestRemoveWaitsForALockToClear(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "locked.csv")
	if err := os.WriteFile(p, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := lockExclusive(t, p)
	go func() {
		time.Sleep(80 * time.Millisecond)
		syscall.CloseHandle(h)
	}()

	if err := NewLocal(dir).Remove(context.Background(), "locked.csv"); err != nil {
		t.Errorf("Remove() = %v, want it to have waited for the lock", err)
	}
}

// Paths past MAX_PATH, which a dated remote tree reaches sooner than one
// expects.
func TestPathsLongerThanMaxPath(t *testing.T) {
	dir := t.TempDir()
	deep := ""
	for len(filepath.Join(dir, deep)) < 300 {
		deep = filepath.Join(deep, "0123456789012345678901234567890123456789")
	}
	l := NewLocal(dir)
	ctx := context.Background()
	name := filepath.ToSlash(filepath.Join(deep, "report.csv"))

	if err := l.MkdirAll(ctx, filepath.ToSlash(deep)); err != nil {
		t.Fatalf("MkdirAll at %d characters: %v", len(filepath.Join(dir, deep)), err)
	}
	if _, err := l.Write(ctx, name, strings.NewReader("payload")); err != nil {
		t.Fatalf("Write at %d characters: %v", len(l.HostPath(name)), err)
	}
	if fi, err := l.Stat(ctx, name); err != nil || fi.Size != 7 {
		t.Errorf("Stat() = %+v, %v", fi, err)
	}
}

// Name lookups have to fold case here, or a destination that already holds
// report.csv would be overwritten by a remote REPORT.CSV without being noticed.
func TestLocalFoldsCaseHere(t *testing.T) {
	if !NewLocal(t.TempDir()).CaseInsensitive() {
		t.Error("CaseInsensitive() = false on Windows")
	}
}

// lockExclusive opens path denying every kind of sharing, the way an
// application still writing a file holds it.
func lockExclusive(t *testing.T, path string) syscall.Handle {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func names(ents []os.DirEntry) []string {
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}
