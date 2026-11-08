// Package lock keeps a job from running in two processes at once.
//
// Two processes running the same job would read the same files and write them
// under the same temporary names: one overwriting the other's half-sent file,
// both sending what one should have, and with post_action delete one taking a
// source away while the other is still reading it. A lock per job, taken before
// the job starts and held until it ends, turns the second into a refusal.
//
// The lock is the operating system's, on a file: flock where there is one,
// LockFileEx on Windows. It belongs to the open file rather than to the file's
// existence, so a process that dies releases it with nothing left to clean up,
// and the file itself is left in place for the next run to lock again.
//
// A lock file is opened for writing, so it is refused when it is anything but
// an ordinary file of its own: a symbolic or hard link planted at its name
// would otherwise have goft overwrite whatever the link points at.
package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"goft/internal/i18n"
)

// ErrHeld reports a lock another process holds.
var ErrHeld = i18n.Errorf("another goft process is running it")

// Lock is a job's lock, held until it is released.
type Lock struct {
	f *os.File
}

// Acquire takes the lock for the job called name, defined in the file origin,
// in dir. origin identifies the file however it is spelled, as
// config.PathKey gives it. When another process holds the lock the error wraps
// [ErrHeld] and says which process, where it can.
func Acquire(dir, name, origin string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, i18n.Errorf("lock directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, FileName(name, origin))
	f, err := openFile(path)
	if err != nil {
		return nil, i18n.Errorf("lock %s: %w", path, err)
	}
	if err := checkFile(f); err != nil {
		_ = f.Close()
		return nil, i18n.Errorf("lock %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		holder := readHolder(f)
		_ = f.Close()
		if errors.Is(err, errWouldBlock) {
			if holder != "" {
				return nil, i18n.Errorf("job %q: %w (pid %s)", name, ErrHeld, holder)
			}
			return nil, i18n.Errorf("job %q: %w", name, ErrHeld)
		}
		return nil, i18n.Errorf("lock %s: %w", path, err)
	}
	// Who holds it, for the error the next process to try will report. The
	// lock is what counts; this is only a courtesy.
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &Lock{f: f}, nil
}

// Release lets the lock go.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unlockFile(l.f)
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}

// FileName is the name of the lock file for a job: the job's name, for a person
// looking in the directory, then a digest of the name and the file defining
// the job.
//
// A job is the name in the file that defines it. Two configurations may each
// have a job of the same name — the service's own and a second one installed
// alongside for testing, say — and those are different jobs that must not stop
// each other starting; one job file read by two processes is the same job,
// whichever main file named it. Names that differ only in case are one name.
//
// A job name is anything a person chose, so whatever is not plainly safe in a
// file name on every platform is written as %XX, keeping names that are
// already plain readable.
func FileName(name, origin string) string {
	name = strings.ToLower(name)
	sum := sha256.Sum256([]byte(name + "\x00" + origin))
	var b strings.Builder
	for _, c := range []byte(name) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return "job-" + b.String() + "-" + hex.EncodeToString(sum[:6]) + ".lock"
}

// readHolder reads the process id the holder left in the file, if any.
func readHolder(f *os.File) string {
	buf := make([]byte, 32)
	n, _ := f.ReadAt(buf, 0)
	pid := strings.TrimSpace(string(buf[:n]))
	if _, err := strconv.Atoi(pid); err != nil {
		return ""
	}
	return pid
}
