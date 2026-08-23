package fsys

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Local is an FS backed by the operating system, rooted at a directory.
type Local struct {
	root string
}

// NewLocal returns an FS rooted at dir.
func NewLocal(dir string) *Local { return &Local{root: dir} }

// HostPath returns the operating system path for a rooted relative name.
// It exists so that post-processing can move a file between two local roots,
// which no single rooted [FS] can express.
func (l *Local) HostPath(name string) string {
	if name == "" {
		return l.root
	}
	return filepath.Join(l.root, filepath.FromSlash(name))
}

// Root returns the directory this FS is rooted at.
func (l *Local) Root() string { return l.root }

// Describe implements FS.
func (l *Local) Describe() string { return l.root }

// CaseInsensitive implements FS. Windows and macOS fold case; on other systems
// the safe assumption is that they do not.
func (l *Local) CaseInsensitive() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

// Close implements FS. A local file system holds no connection.
func (l *Local) Close() error { return nil }

// List implements FS.
func (l *Local) List(_ context.Context, dir string) ([]Entry, error) {
	des, err := os.ReadDir(l.HostPath(dir))
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			// The entry vanished between listing and stat; skip it.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		out = append(out, Entry{
			Name:      de.Name(),
			Size:      info.Size(),
			ModTime:   info.ModTime(),
			IsDir:     info.IsDir(),
			IsRegular: info.Mode().IsRegular(),
		})
	}
	return out, nil
}

// Stat implements FS.
func (l *Local) Stat(_ context.Context, name string) (FileInfo, error) {
	fi, err := os.Stat(l.HostPath(name))
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Size: fi.Size(), ModTime: fi.ModTime()}, nil
}

// Open implements FS.
func (l *Local) Open(_ context.Context, name string) (io.ReadCloser, error) {
	return os.Open(l.HostPath(name))
}

// Write implements FS, truncating any existing file.
func (l *Local) Write(_ context.Context, name string, r io.Reader) (int64, error) {
	f, err := os.OpenFile(l.HostPath(name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, transferredFileMode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if err != nil {
		f.Close()
		return n, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return n, err
	}
	return n, f.Close()
}

// MkdirAll implements FS.
func (l *Local) MkdirAll(_ context.Context, dir string) error {
	return os.MkdirAll(l.HostPath(dir), 0o755)
}

// Rename implements FS. Crossing a volume boundary is absorbed here so that
// callers only ever see a rename.
func (l *Local) Rename(_ context.Context, from, to string) error {
	return MoveFile(l.HostPath(from), l.HostPath(to))
}

// Remove implements FS.
func (l *Local) Remove(_ context.Context, name string) error {
	return withLockRetry(func() error {
		err := os.Remove(l.HostPath(name))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	})
}

// MoveFile renames from to to, falling back to copy+remove when the two live on
// different volumes. The error returned by rename is deliberately not
// inspected: Linux reports EXDEV while Windows reports ERROR_NOT_SAME_DEVICE,
// and neither is portable to test for.
func MoveFile(from, to string) error {
	if err := withLockRetry(func() error { return os.Rename(from, to) }); err == nil {
		return nil
	}
	if err := copyFile(from, to); err != nil {
		return err
	}
	return withLockRetry(func() error { return os.Remove(from) })
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, transferredFileMode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		_ = os.Remove(to)
		return err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		_ = os.Remove(to)
		return err
	}
	return dst.Close()
}

// withLockRetry retries an operation that Windows may reject while another
// process still holds the file open (ERROR_SHARING_VIOLATION). On other systems
// the first attempt is normally the only one.
func withLockRetry(op func() error) error {
	const attempts = 4
	var err error
	for i := 0; i < attempts; i++ {
		if err = op(); err == nil || !isSharingViolation(err) {
			return err
		}
		time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
	}
	return err
}

// isSharingViolation reports whether err looks like a transient Windows lock.
// The check is by message so that no OS specific build tags are needed.
func isSharingViolation(err error) bool {
	if err == nil || runtime.GOOS != "windows" {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "being used by another process") ||
		strings.Contains(msg, "sharing violation") ||
		strings.Contains(msg, "access is denied")
}

var _ FS = (*Local)(nil)
