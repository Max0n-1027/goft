package lock

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"goft/internal/i18n"
)

var errWouldBlock = windows.ERROR_LOCK_VIOLATION

// DefaultDir is where locks are kept when the configuration does not say:
// goft\locks under ProgramData, created if need be.
//
// It is the same directory for every account. The temporary directory would
// not be: the service runs as LocalSystem, whose temporary directory is not an
// administrator's, and a job run by hand while the service ran it would then
// find no lock to be refused by.
func DefaultDir() (string, error) {
	base := os.Getenv("ProgramData")
	dir := filepath.Join(base, "goft", "locks")
	if base == "" {
		dir = filepath.Join(os.TempDir(), "goft")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", i18n.Errorf("lock directory %s: %w", dir, err)
	}
	return dir, nil
}

// openFile opens or creates a lock file. A reparse point at its name — a
// symbolic link or a junction — is opened as itself rather than followed, so
// that checkFile can refuse it.
func openFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// checkFile refuses a lock file that is not an ordinary file with one name.
func checkFile(f *os.File) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return i18n.Errorf("not an ordinary file")
	}
	if info.NumberOfLinks > 1 {
		return i18n.Errorf("the file has other names (hard links); remove it")
	}
	return nil
}

// The locked range lies past anything the file will hold, so the process id
// written at its start stays readable to the process that is refused.
const (
	rangeOffsetHigh = 1
	rangeLength     = 1
)

// lockFile takes an exclusive lock without waiting. A lock belongs to the
// handle, so even two opens in one process exclude each other.
func lockFile(f *os.File) error {
	ol := &windows.Overlapped{OffsetHigh: rangeOffsetHigh}
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, rangeLength, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return errWouldBlock
	}
	return err
}

func unlockFile(f *os.File) error {
	ol := &windows.Overlapped{OffsetHigh: rangeOffsetHigh}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, rangeLength, 0, ol)
}
