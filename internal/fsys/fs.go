/*
Package fsys abstracts the local file system and the remote protocols behind
one interface, so that the transfer engine never needs to know which side is
local and which is remote.

Every [FS] is rooted at a directory, and every name passed to it is a slash
separated path relative to that root, on all platforms. Only the local
implementation converts to and from the host convention; the engine and the
remote implementations deal in forward slashes throughout, which keeps a
Windows path from ever reaching a server.

Use [NewLocal] for a directory and [NewRemote] for a server. Each call to
NewRemote opens an independent connection, which is how the engine keeps the
number of live connections equal to the number of workers.

Implementations are expected to behave the same in three respects the engine
depends on: a missing file or directory reports an error satisfying
errors.Is(err, fs.ErrNotExist), [FS.Write] truncates a file that already
exists, and [Entry.IsRegular] is true only for ordinary files. The conformance
suite in the tests checks all of it against every protocol.

An implementation may also refuse a name outright, with an error wrapping
fs.ErrInvalid, when its file system would store the file under a different name
than the one asked for. [Local] does this on Windows; see [checkStorableName]
for what it covers and why it cannot be left to verification to catch.
*/
package fsys

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"time"
)

// FileInfo is the subset of stat information goft relies on.
type FileInfo struct {
	// Size is the file size in bytes, which the length verification compares.
	Size int64
	// ModTime is when the file last changed, which the settling check watches.
	ModTime time.Time
}

// Entry is one directory listing item.
type Entry struct {
	// Name is the base name, without any directory part.
	Name string
	// Size is the file size in bytes, and ModTime when it last changed. Both
	// feed the settling check, so a protocol that reports a coarse ModTime
	// makes that check coarser too.
	Size    int64
	ModTime time.Time
	// IsDir marks a directory, which a recursive scan descends into.
	IsDir bool
	// IsRegular is an allow-list: only regular files are ever transferred.
	// Symlinks, devices and anything a protocol cannot classify are false.
	IsRegular bool
}

// FS is a rooted file system. Every name is a slash separated path relative to
// the root, so the same engine code works for a local directory and for a
// remote share.
type FS interface {
	// List returns the entries of dir ("" is the root). A directory that does
	// not exist yields an error satisfying errors.Is(err, fs.ErrNotExist).
	List(ctx context.Context, dir string) ([]Entry, error)
	// Stat reports on a single file. Missing files yield fs.ErrNotExist.
	Stat(ctx context.Context, name string) (FileInfo, error)
	// Open opens a file for reading.
	Open(ctx context.Context, name string) (io.ReadCloser, error)
	// Write creates name, truncating it if it already exists, and returns the
	// number of bytes written.
	Write(ctx context.Context, name string, r io.Reader) (int64, error)
	// MkdirAll creates dir and any missing parents.
	MkdirAll(ctx context.Context, dir string) error
	// Rename moves from to to within this file system.
	Rename(ctx context.Context, from, to string) error
	// Remove deletes a file.
	Remove(ctx context.Context, name string) error
	// CaseInsensitive reports whether name lookups must also try a lower-cased
	// match, because the underlying file system folds case.
	CaseInsensitive() bool
	// Describe returns a short location for logs and console output.
	Describe() string
	// Close releases the connection, if any.
	Close() error
}

// transferredFileMode is the mode new files are created with, by the
// implementations that get to choose one: [Local] and the SMB share. Data that
// goft moves is usually read by something else afterwards, so it is created the
// way scp or rsync would leave it rather than owner-only; the process umask
// still applies on top. Windows ignores the mode entirely, and a file written
// there takes the permissions of the directory it lands in.
//
// sftp and ftp are not covered by this. Neither implementation sends a mode:
// the protocols let one be given at creation, but the destination's own idea of
// what a new file should look like is a better answer than a Unix mode invented
// by the client. A Windows sftp server is the case in point — it discards the
// mode and lets the file inherit the permissions of the directory it lands in,
// which is what the operator set that directory up for.
const transferredFileMode = 0o644

// TempSuffix is appended while a transfer is in flight. A file carrying it is
// never a transfer candidate, regardless of the configured filters.
const TempSuffix = ".goft.tmp"

// TempName returns the in-flight name for a destination file.
func TempName(name string) string { return name + TempSuffix }

// IsTempName reports whether name is one of goft's in-flight files.
func IsTempName(name string) bool { return strings.HasSuffix(name, TempSuffix) }

// Join builds a rooted relative path from its segments, dropping empty ones.
func Join(elems ...string) string {
	parts := make([]string, 0, len(elems))
	for _, e := range elems {
		if e != "" {
			parts = append(parts, e)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return path.Join(parts...)
}

// Segments splits a rooted relative path into its parts, dropping empty ones.
// Protocols that can only create one directory at a time walk it.
func Segments(p string) []string {
	var out []string
	for _, part := range strings.Split(p, "/") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Dir returns the parent of a rooted relative path, "" for top level entries.
func Dir(name string) string {
	d := path.Dir(name)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// HostPather is implemented by file systems backed by the operating system.
// Moving a file between two roots cannot be expressed by a single rooted [FS],
// so post-processing reaches for the underlying paths through this interface.
type HostPather interface {
	HostPath(name string) string
}

// HostPathOf returns the operating system path for name, if f is local.
func HostPathOf(f FS, name string) (string, bool) {
	hp, ok := f.(HostPather)
	if !ok {
		return "", false
	}
	return hp.HostPath(name), true
}

// ErrStalled reports an operation abandoned because its connection moved no
// data for remote.io_timeout. The connection is closed to get the waiting call
// back, so everything else on it fails the same way, at once.
var ErrStalled = errors.New("the connection stalled")
