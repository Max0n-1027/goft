package fsys

import (
	"context"
	"path"
	"strings"
	"testing"

	"goft/internal/ftptest"
	"goft/internal/sftptest"
)

// checkCreatesMissingRoot asserts that an FS rooted at a directory that does
// not exist yet creates it when asked to create "" or anything below.
//
// goft test tells the operator a missing destination "will be created on the
// first transfer". FTP and SMB only ever created the levels below their root,
// so on those the first transfer failed instead: every file on FTP, and on
// SMB whenever no subdirectory happened to create the root on the way.
func checkCreatesMissingRoot(t *testing.T, at func(t *testing.T, root string) FS, base string) {
	t.Helper()
	ctx := context.Background()

	t.Run("the root itself", func(t *testing.T) {
		f := at(t, path.Join(base, "missing-root", "deeper"))
		defer f.Close()
		if err := f.MkdirAll(ctx, ""); err != nil {
			t.Fatalf("MkdirAll(\"\"): %v", err)
		}
		if _, err := f.Write(ctx, "top.csv", strings.NewReader("id\n")); err != nil {
			t.Fatalf("a file at the top of the new root: %v", err)
		}
		if _, err := f.Stat(ctx, "top.csv"); err != nil {
			t.Error(err)
		}
	})

	t.Run("below the root", func(t *testing.T) {
		f := at(t, path.Join(base, "missing-too", "deeper"))
		defer f.Close()
		if err := f.MkdirAll(ctx, "2026-08/day-01"); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if _, err := f.Write(ctx, "2026-08/day-01/a.csv", strings.NewReader("id\n")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an existing root is fine", func(t *testing.T) {
		f := at(t, path.Join(base, "missing-root", "deeper"))
		defer f.Close()
		if err := f.MkdirAll(ctx, ""); err != nil {
			t.Fatalf("MkdirAll on a root that now exists: %v", err)
		}
	})
}

func TestFTPCreatesAMissingRoot(t *testing.T) {
	remote := ftptest.Start(t, t.TempDir())
	checkCreatesMissingRoot(t, func(t *testing.T, root string) FS {
		r := remote
		r.Path = root
		f, err := NewRemote(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}, "/")
}

func TestSFTPCreatesAMissingRoot(t *testing.T) {
	remote := sftptest.Start(t, t.TempDir())
	checkCreatesMissingRoot(t, func(t *testing.T, root string) FS {
		r := remote
		r.Path = root
		f, err := NewRemote(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}, remote.Path)
}

func TestLocalCreatesAMissingRoot(t *testing.T) {
	checkCreatesMissingRoot(t, func(t *testing.T, root string) FS {
		return NewLocal(root)
	}, t.TempDir())
}

// TestLiveCreatesAMissingRoot runs the same check against a real server, which
// is the only way SMB is covered. It works under GOFT_LIVE_PATH and removes
// everything it made.
func TestLiveCreatesAMissingRoot(t *testing.T) {
	remote := liveRemote(t)
	base := path.Join(remote.Path, "goft-missing-root-test")
	t.Cleanup(func() {
		f, err := NewRemote(context.Background(), remote)
		if err != nil {
			return
		}
		defer f.Close()
		for _, p := range []string{
			"missing-root/deeper/top.csv", "missing-root/deeper", "missing-root",
			"missing-too/deeper/2026-08/day-01/a.csv", "missing-too/deeper/2026-08/day-01",
			"missing-too/deeper/2026-08", "missing-too/deeper", "missing-too", "",
		} {
			_ = f.Remove(context.Background(), path.Join("goft-missing-root-test", p))
		}
	})
	checkCreatesMissingRoot(t, func(t *testing.T, root string) FS {
		r := remote
		r.Path = root
		f, err := NewRemote(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}, base)
}
