//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package lock

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestASymbolicLinkAtTheLockIsNotFollowed(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(victim, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, FileName("a", origin))); err != nil {
		t.Fatal(err)
	}
	if l, err := Acquire(dir, "a", origin); err == nil {
		l.Release()
		t.Fatal("Acquire took a lock through a symbolic link")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep me\n" {
		t.Errorf("the file the link pointed at was written: %q", b)
	}
}

func TestAHardLinkAtTheLockIsRefused(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "precious")
	if err := os.WriteFile(victim, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(victim, filepath.Join(dir, FileName("a", origin))); err != nil {
		t.Skipf("cannot make a hard link here: %v", err)
	}
	_, err := Acquire(dir, "a", origin)
	if err == nil || !strings.Contains(err.Error(), "hard links") {
		t.Fatalf("Acquire = %v, want a lock file with other names refused", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep me\n" {
		t.Errorf("the linked file was written: %q", b)
	}
}

func TestADirectoryAtTheLockIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, FileName("a", origin)), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir, "a", origin); err == nil {
		t.Fatal("Acquire took a directory for a lock file")
	}
}

func TestTheDefaultDirectoryIsThisAccountsOwn(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	want := filepath.Join(tmp, "goft-"+strconv.Itoa(os.Geteuid()))

	dir, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != want {
		t.Errorf("DefaultDir() = %s, want %s", dir, want)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want it closed to other accounts", fi.Mode().Perm())
	}
	// Asking again finds the same directory acceptable.
	if again, err := DefaultDir(); err != nil || again != dir {
		t.Errorf("DefaultDir() again = %s, %v", again, err)
	}
}

func TestTheDefaultDirectoryIsRefusedWhenOthersCouldWriteIt(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir := filepath.Join(tmp, "goft-"+strconv.Itoa(os.Geteuid()))
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultDir(); err == nil || !strings.Contains(err.Error(), "service.lock_dir") {
		t.Errorf("DefaultDir() = %v, want a directory open to others refused", err)
	}
}

func TestTheDefaultDirectoryIsRefusedWhenItIsALink(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	if err := os.Symlink(t.TempDir(), filepath.Join(tmp, "goft-"+strconv.Itoa(os.Geteuid()))); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultDir(); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("DefaultDir() = %v, want a link in its place refused", err)
	}
}

func TestTheDefaultDirectoryCannotBeMadeUnderAFile(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(tmp, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	if _, err := DefaultDir(); err == nil {
		t.Error("DefaultDir() made a directory under a file")
	}
}
