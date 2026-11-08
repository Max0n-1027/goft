//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package lock

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"goft/internal/i18n"
)

var errWouldBlock = syscall.EWOULDBLOCK

// DefaultDir is where locks are kept when the configuration does not say: a
// directory of this account's own under the temporary directory, created if
// need be.
//
// It is per account because a directory shared with other accounts could be
// made by any of them first, and filled with links for goft to write through.
// A job is therefore protected from a second run under the same account — the
// service's own, which is how it is normally run by hand as well. The
// directory is refused unless it is a real directory, this account's, and
// closed to everyone else.
func DefaultDir() (string, error) {
	uid := os.Geteuid()
	dir := filepath.Join(os.TempDir(), "goft-"+strconv.Itoa(uid))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", i18n.Errorf("lock directory %s: %w", dir, err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return "", i18n.Errorf("lock directory %s: %w", dir, err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !fi.IsDir():
		return "", i18n.Errorf("lock directory %s is not a directory; set service.lock_dir", dir)
	case !ok || int(st.Uid) != uid:
		return "", i18n.Errorf("lock directory %s belongs to another account; set service.lock_dir", dir)
	case fi.Mode().Perm()&0o077 != 0:
		return "", i18n.Errorf("lock directory %s is open to other accounts (%v); set service.lock_dir", dir, fi.Mode().Perm())
	}
	return dir, nil
}

// openFile opens or creates a lock file, refusing a symbolic link at its name.
func openFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
}

// checkFile refuses a lock file that is not an ordinary file with one name.
func checkFile(f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return i18n.Errorf("not an ordinary file")
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
		return i18n.Errorf("the file has other names (hard links); remove it")
	}
	return nil
}

// lockFile takes an exclusive flock without waiting. flock belongs to the open
// file description, so even two opens in one process exclude each other.
func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EAGAIN) {
		return errWouldBlock
	}
	return err
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
