package fsys

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goft/internal/config"
	"goft/internal/ftptest"
)

func TestFTPConformance(t *testing.T) {
	root := t.TempDir()
	remote := ftptest.Start(t, root)
	runFSConformance(t, func(t *testing.T) FS {
		f, err := NewRemote(context.Background(), remote)
		if err != nil {
			t.Fatal(err)
		}
		return f
	})
}

// dialFTP opens a connection to a server rooted at a fresh directory.
func dialFTP(t *testing.T) (FS, string) {
	t.Helper()
	root := t.TempDir()
	f, err := NewRemote(context.Background(), ftptest.Start(t, root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, root
}

func TestFTPTransfersBinaryUnaltered(t *testing.T) {
	f, root := dialFTP(t)
	ctx := context.Background()

	// Bytes that ASCII mode would rewrite. Getting the transfer type wrong
	// corrupts exactly this and nothing else, so a text file would not notice.
	payload := []byte("line\r\nline\nnul\x00byte\r\n\x1a")
	if _, err := f.Write(ctx, "binary.dat", bytesReader(payload)); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(root, "binary.dat"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("content = %q, want it byte for byte: %q", got, payload)
	}
}

func TestFTPListReportsAMissingDirectory(t *testing.T) {
	f, _ := dialFTP(t)

	if _, err := f.List(context.Background(), "absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("List() = %v, want fs.ErrNotExist", err)
	}
	// This server answers a missing path with an error of its own, so the check
	// passes without the client's own empty-listing defence being reached.
	// vsftpd instead answers with an empty listing and no error, which is what
	// that defence is for; only the live suite exercises it.
}

func TestFTPDistinguishesEmptyFromMissing(t *testing.T) {
	f, root := dialFTP(t)
	ctx := context.Background()
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := f.List(ctx, "empty")
	if err != nil {
		t.Fatalf("List of an empty directory = %v, want it to succeed", err)
	}
	if len(entries) != 0 {
		t.Errorf("List() = %v, want nothing", entries)
	}
}

func TestFTPCreatesNestedDirectories(t *testing.T) {
	f, root := dialFTP(t)
	ctx := context.Background()

	// FTP creates one level at a time, so a tree has to be walked.
	if err := f.MkdirAll(ctx, "2026/08/week3"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "2026", "08", "week3")); err != nil || !fi.IsDir() {
		t.Errorf("the tree was not created: %v", err)
	}
	// Asking again must be harmless: several workers reach for the same one.
	if err := f.MkdirAll(ctx, "2026/08/week3"); err != nil {
		t.Errorf("MkdirAll on an existing tree = %v, want nil", err)
	}
}

func TestFTPRemovesFilesAndDirectories(t *testing.T) {
	f, root := dialFTP(t)
	ctx := context.Background()

	if err := f.MkdirAll(ctx, "probe"); err != nil {
		t.Fatal(err)
	}
	// The write probe in `goft test` takes away a directory, so one call has to
	// cover both. This server accepts DELE on a directory; vsftpd requires RMD,
	// and only the live suite reaches that fallback.
	if err := f.Remove(ctx, "probe"); err != nil {
		t.Fatalf("Remove of a directory = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "probe")); !os.IsNotExist(err) {
		t.Error("the directory is still there")
	}
}

func TestFTPStatReportsSizeAndAbsence(t *testing.T) {
	f, _ := dialFTP(t)
	ctx := context.Background()

	if _, err := f.Write(ctx, "sized.bin", strings.NewReader(strings.Repeat("x", 4096))); err != nil {
		t.Fatal(err)
	}
	// FTP has no stat command; this goes through MLST or SIZE.
	info, err := f.Stat(ctx, "sized.bin")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 4096 {
		t.Errorf("Stat size = %d, want 4096", info.Size)
	}
	if _, err := f.Stat(ctx, "absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat of a missing file = %v, want fs.ErrNotExist", err)
	}
}

func TestFTPWriteReportsWhatItSent(t *testing.T) {
	f, _ := dialFTP(t)

	// STOR does not report a byte count, so the client counts as it streams;
	// the length verification compares against it.
	n, err := f.Write(context.Background(), "counted.bin", strings.NewReader("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 10 {
		t.Errorf("Write reported %d bytes, want 10", n)
	}
}

func TestFTPRefusesBadCredentials(t *testing.T) {
	remote := ftptest.Start(t, t.TempDir())
	remote.Password = config.Secret("wrong")

	if _, err := NewRemote(context.Background(), remote); err == nil {
		t.Fatal("want an error for a rejected login")
	}
}

func TestFTPRequiresCredentials(t *testing.T) {
	remote := ftptest.Start(t, t.TempDir())
	remote.User, remote.Password = "", ""

	_, err := NewRemote(context.Background(), remote)
	if err == nil {
		t.Fatal("want an error when nothing supplies credentials")
	}
	// Anonymous access is never assumed on the operator's behalf.
	if !strings.Contains(err.Error(), "credentials") {
		t.Errorf("error = %v, want it to explain that no credentials were found", err)
	}
}

func bytesReader(b []byte) *strings.Reader { return strings.NewReader(string(b)) }
