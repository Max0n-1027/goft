package lock

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// origin stands for the file defining the jobs these tests lock.
const origin = "/etc/goft/goft.yaml"

func TestASecondHolderIsRefused(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir, "invoice-upload", origin)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Acquire(dir, "invoice-upload", origin)
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("second Acquire = %v, want ErrHeld", err)
	}
	if !strings.Contains(err.Error(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Errorf("error = %v, want it to name the process holding the lock", err)
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(dir, "invoice-upload", origin)
	if err != nil {
		t.Fatalf("Acquire after Release = %v", err)
	}
	defer again.Release()
}

func TestJobsLockSeparately(t *testing.T) {
	dir := t.TempDir()
	a, err := Acquire(dir, "a", origin)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := Acquire(dir, "b", origin)
	if err != nil {
		t.Fatalf("a lock on one job held another: %v", err)
	}
	defer b.Release()
}

func TestTheLockDirectoryIsCreated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run", "goft")
	l, err := Acquire(dir, "a", origin)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if _, err := os.Stat(filepath.Join(dir, FileName("a", origin))); err != nil {
		t.Errorf("lock file: %v", err)
	}
}

func TestReleaseIsSafeTwice(t *testing.T) {
	l, err := Acquire(t.TempDir(), "a", origin)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Errorf("second Release = %v", err)
	}
	var none *Lock
	if err := none.Release(); err != nil {
		t.Errorf("Release of no lock = %v", err)
	}
}

func TestFileNameKeepsPlainNamesAndEscapesTheRest(t *testing.T) {
	for name, want := range map[string]string{
		"invoice-upload_2.v1": "job-invoice-upload_2.v1-",
		"a/b":                 "job-a%2Fb-",
		"日次":                  "job-%E6%97%A5%E6%AC%A1-",
		"c:d":                 "job-c%3Ad-",
	} {
		got := FileName(name, origin)
		if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, ".lock") || len(got) != len(want)+12+len(".lock") {
			t.Errorf("FileName(%q) = %q, want %q, 12 hex digits and .lock", name, got, want)
		}
	}
}

func TestAJobIsItsNameInTheFileDefiningIt(t *testing.T) {
	dir := t.TempDir()
	held, err := Acquire(dir, "upload", "/etc/goft/goft.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	// Another configuration's job of the same name is another job.
	other, err := Acquire(dir, "upload", "/etc/goft/test.yaml")
	if err != nil {
		t.Fatalf("a job of the same name in another file was refused: %v", err)
	}
	other.Release()

	// The same job asked for under another spelling of its name is not.
	if _, err := Acquire(dir, "Upload", "/etc/goft/goft.yaml"); !errors.Is(err, ErrHeld) {
		t.Errorf("Acquire under the name in another case = %v, want ErrHeld", err)
	}
}

func TestAnUnwritableDirectoryIsReported(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(file, "a", origin); err == nil || errors.Is(err, ErrHeld) {
		t.Errorf("Acquire under a file = %v, want an error that is not ErrHeld", err)
	}
}
