package fsys

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
)

// runFSConformance exercises the behaviour the engine relies on from every
// protocol. newFS must return a fresh connection rooted at a directory the test
// may write to; the test cleans up after itself.
//
// Both the in-process sftp server and a live server are driven through this,
// so a protocol only has to be pointed at it to be covered.
func runFSConformance(t *testing.T, newFS func(t *testing.T) FS) {
	ctx := context.Background()

	t.Run("write then read back", func(t *testing.T) {
		f := newFS(t)
		defer f.Close()
		defer f.Remove(ctx, "conformance.txt")

		n, err := f.Write(ctx, "conformance.txt", strings.NewReader("payload"))
		if err != nil {
			t.Fatal(err)
		}
		if n != 7 {
			t.Errorf("Write reported %d bytes, want 7", n)
		}

		rc, err := f.Open(ctx, "conformance.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		body, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "payload" {
			t.Errorf("content = %q, want payload", body)
		}
	})

	t.Run("write truncates", func(t *testing.T) {
		// The engine writes its temporary file without deleting a leftover
		// first, so every protocol has to truncate on create.
		f := newFS(t)
		defer f.Close()
		defer f.Remove(ctx, "truncate.txt")

		if _, err := f.Write(ctx, "truncate.txt", strings.NewReader("a much longer body")); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(ctx, "truncate.txt", strings.NewReader("short")); err != nil {
			t.Fatal(err)
		}
		rc, err := f.Open(ctx, "truncate.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		body, _ := io.ReadAll(rc)
		if string(body) != "short" {
			t.Errorf("content = %q, want the file truncated to 'short'", body)
		}
	})

	t.Run("stat reports size", func(t *testing.T) {
		f := newFS(t)
		defer f.Close()
		defer f.Remove(ctx, "sized.bin")

		payload := bytes.Repeat([]byte("x"), 4096)
		if _, err := f.Write(ctx, "sized.bin", bytes.NewReader(payload)); err != nil {
			t.Fatal(err)
		}
		info, err := f.Stat(ctx, "sized.bin")
		if err != nil {
			t.Fatal(err)
		}
		// The length verification method compares exactly this.
		if info.Size != int64(len(payload)) {
			t.Errorf("Stat size = %d, want %d", info.Size, len(payload))
		}
	})

	t.Run("missing entries report ErrNotExist", func(t *testing.T) {
		f := newFS(t)
		defer f.Close()

		// The engine branches on this to decide whether to create a directory
		// or to leave an existing file alone.
		if _, err := f.Stat(ctx, "definitely-absent"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat of a missing file = %v, want fs.ErrNotExist", err)
		}
		if _, err := f.List(ctx, "definitely-absent-dir"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("List of a missing directory = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("directories and listings", func(t *testing.T) {
		f := newFS(t)
		defer f.Close()
		defer func() {
			f.Remove(ctx, "nested/deep/file.txt")
			f.Remove(ctx, "nested/deep")
			f.Remove(ctx, "nested")
		}()

		if err := f.MkdirAll(ctx, "nested/deep"); err != nil {
			t.Fatal(err)
		}
		// Creating a directory that already exists must be harmless: several
		// workers may reach for the same one.
		if err := f.MkdirAll(ctx, "nested/deep"); err != nil {
			t.Fatalf("MkdirAll on an existing directory = %v, want nil", err)
		}
		if _, err := f.Write(ctx, "nested/deep/file.txt", strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}

		entries, err := f.List(ctx, "nested/deep")
		if err != nil {
			t.Fatal(err)
		}
		var found *Entry
		for i := range entries {
			if entries[i].Name == "file.txt" {
				found = &entries[i]
			}
		}
		if found == nil {
			t.Fatalf("List = %+v, want it to contain file.txt", entries)
		}
		if !found.IsRegular || found.IsDir {
			t.Errorf("entry = %+v, want a regular file: only these are transferred", *found)
		}
		if found.Size != 1 {
			t.Errorf("entry size = %d, want 1", found.Size)
		}
	})

	t.Run("rename publishes a file", func(t *testing.T) {
		f := newFS(t)
		defer f.Close()
		defer f.Remove(ctx, "published.txt")

		if _, err := f.Write(ctx, "published.txt"+TempSuffix, strings.NewReader("done")); err != nil {
			t.Fatal(err)
		}
		if err := f.Rename(ctx, "published.txt"+TempSuffix, "published.txt"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Stat(ctx, "published.txt"+TempSuffix); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the temporary name should be gone after a rename, got %v", err)
		}
		if _, err := f.Stat(ctx, "published.txt"); err != nil {
			t.Errorf("the published file is missing: %v", err)
		}
	})

	t.Run("remove a directory", func(t *testing.T) {
		// `goft test` proves write access by creating a directory and taking
		// it away again, so this is not merely tidiness.
		f := newFS(t)
		defer f.Close()

		if err := f.MkdirAll(ctx, "probe-dir"); err != nil {
			t.Fatal(err)
		}
		if err := f.Remove(ctx, "probe-dir"); err != nil {
			t.Fatalf("Remove of a directory = %v, want it removed", err)
		}
		if _, err := f.List(ctx, "probe-dir"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the directory is still there: List = %v", err)
		}
	})

	t.Run("remove", func(t *testing.T) {
		f := newFS(t)
		defer f.Close()

		if _, err := f.Write(ctx, "doomed.txt", strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
		if err := f.Remove(ctx, "doomed.txt"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Stat(ctx, "doomed.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat after Remove = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("mkdir over a file fails", func(t *testing.T) {
		// A directory that could not be created must be reported, not taken
		// for one that was already there. On FTP that decision once rested on
		// a listing, which vsftpd answers with an empty success even for a path
		// that is not a directory, so the live suite is where this bites.
		f := newFS(t)
		defer f.Close()
		defer f.Remove(ctx, "blocker")

		if _, err := f.Write(ctx, "blocker", strings.NewReader("a file, not a directory")); err != nil {
			t.Fatal(err)
		}
		if err := f.MkdirAll(ctx, "blocker"); err == nil {
			t.Error("MkdirAll reported success for a directory it could not create")
		}
	})
}
