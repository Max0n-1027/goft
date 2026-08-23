package fsys

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/textproto"
	"path"
	"strconv"
	"time"

	goftp "github.com/jlaffaye/ftp"

	"goft/internal/config"
)

type ftpFS struct {
	root string
	desc string
	conn *goftp.ServerConn
}

func newFTP(ctx context.Context, r config.Remote) (FS, error) {
	res, err := resolveFTP(r)
	if err != nil {
		return nil, err
	}
	if res.User == "" || !res.Password.IsSet() {
		return nil, fmt.Errorf("no ftp credentials for %s: set remote.user and remote.password, or add a machine entry to ~/.netrc", r.Host)
	}

	addr := net.JoinHostPort(res.Host, strconv.Itoa(res.Port))
	conn, err := goftp.Dial(addr,
		goftp.DialWithContext(ctx),
		goftp.DialWithTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	// Login negotiates features and switches the connection to binary mode
	// (TYPE I). That matters: in ASCII mode the server rewrites line endings,
	// so sizes and hashes would never match.
	if err := conn.Login(res.User, string(res.Password)); err != nil {
		_ = conn.Quit()
		return nil, fmt.Errorf("ftp login as %s on %s: %w", res.User, addr, err)
	}

	return &ftpFS{
		root: r.Path,
		desc: fmt.Sprintf("ftp://%s%s", addr, r.Path),
		conn: conn,
	}, nil
}

func (f *ftpFS) abs(name string) string {
	if name == "" {
		return f.root
	}
	return path.Join(f.root, name)
}

// Describe implements FS.
func (f *ftpFS) Describe() string { return f.desc }

// CaseInsensitive implements FS. FTP servers are usually backed by a POSIX
// file system; assuming otherwise would skip transfers that should happen.
func (f *ftpFS) CaseInsensitive() bool { return false }

// List implements FS.
//
// A missing directory has to be reported as fs.ErrNotExist, because that is how
// the engine decides to create it. FTP does not cooperate: LIST on a path that
// does not exist succeeds with an empty listing on common servers, vsftpd among
// them. So an empty listing is checked once more before it is believed.
func (f *ftpFS) List(_ context.Context, dir string) ([]Entry, error) {
	abs := f.abs(dir)
	entries, err := f.conn.List(abs)
	if err != nil {
		return nil, translateFTPError(err)
	}
	if len(entries) == 0 {
		exists, err := f.dirExists(abs)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("%w: %s", fs.ErrNotExist, abs)
		}
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			continue
		}
		out = append(out, Entry{
			Name:      e.Name,
			Size:      int64(e.Size),
			ModTime:   e.Time,
			IsDir:     e.Type == goftp.EntryTypeFolder,
			IsRegular: e.Type == goftp.EntryTypeFile,
		})
	}
	return out, nil
}

// dirExists distinguishes an empty directory from one that is not there.
//
// The extra round trip is only paid when a listing came back empty, which for a
// destination directory means at most once per cycle.
func (f *ftpFS) dirExists(abs string) (bool, error) {
	if abs == "" || abs == "/" || abs == "." {
		return true, nil
	}
	if e, err := f.conn.GetEntry(abs); err == nil {
		return e.Type == goftp.EntryTypeFolder, nil
	}
	// No MLST on this server: look for the name in its parent instead.
	parent, base := path.Dir(abs), path.Base(abs)
	entries, err := f.conn.List(parent)
	if err != nil {
		return false, translateFTPError(err)
	}
	for _, e := range entries {
		if e.Name == base {
			return e.Type == goftp.EntryTypeFolder, nil
		}
	}
	return false, nil
}

// Stat implements FS.
//
// FTP has no stat command. MLST provides one where the server supports it;
// otherwise the size comes from SIZE and the timestamp from a directory
// listing, whose resolution is server dependent and can be as coarse as a
// minute. Nothing in goft relies on a remote timestamp for correctness, but a
// recv job settling files off an FTP server should allow for it.
func (f *ftpFS) Stat(_ context.Context, name string) (FileInfo, error) {
	abs := f.abs(name)

	if e, err := f.conn.GetEntry(abs); err == nil {
		return FileInfo{Size: int64(e.Size), ModTime: e.Time}, nil
	}

	size, err := f.conn.FileSize(abs)
	if err != nil {
		return FileInfo{}, translateFTPError(err)
	}
	info := FileInfo{Size: size}
	if t, err := f.conn.GetTime(abs); err == nil {
		info.ModTime = t
	}
	return info, nil
}

// Open implements FS.
func (f *ftpFS) Open(_ context.Context, name string) (io.ReadCloser, error) {
	resp, err := f.conn.Retr(f.abs(name))
	if err != nil {
		return nil, translateFTPError(err)
	}
	return resp, nil
}

// Write implements FS. STOR replaces any existing file.
func (f *ftpFS) Write(_ context.Context, name string, r io.Reader) (int64, error) {
	counter := &countingReader{r: r}
	if err := f.conn.Stor(f.abs(name), counter); err != nil {
		return counter.n, translateFTPError(err)
	}
	return counter.n, nil
}

// MkdirAll implements FS. FTP only creates one directory at a time, and
// reports an error when it already exists, so each level is attempted in turn
// and existing levels are tolerated.
func (f *ftpFS) MkdirAll(ctx context.Context, dir string) error {
	if dir == "" {
		return nil
	}
	var built string
	for _, part := range Segments(dir) {
		built = path.Join(built, part)
		if err := f.conn.MakeDir(f.abs(built)); err != nil {
			if _, statErr := f.conn.List(f.abs(built)); statErr != nil {
				return translateFTPError(err)
			}
		}
	}
	return nil
}

// Rename implements FS.
func (f *ftpFS) Rename(_ context.Context, from, to string) error {
	return translateFTPError(f.conn.Rename(f.abs(from), f.abs(to)))
}

// Remove implements FS.
//
// DELE only removes files. Directories need RMD, and goft removes one of those
// during the write probe in `goft test`, so both are tried.
func (f *ftpFS) Remove(_ context.Context, name string) error {
	abs := f.abs(name)
	err := f.conn.Delete(abs)
	if err == nil {
		return nil
	}
	if rmdErr := f.conn.RemoveDir(abs); rmdErr == nil {
		return nil
	}
	return translateFTPError(err)
}

// Close implements FS.
func (f *ftpFS) Close() error { return f.conn.Quit() }

// countingReader records how many bytes were handed to the server, since Stor
// does not report it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// translateFTPError maps the "file unavailable" replies onto fs.ErrNotExist so
// that the engine can treat every protocol the same way.
//
// Reply 550 also covers permission denied, which is folded in here as well.
// That is deliberate: goft only consults this to decide whether something is
// missing before creating or replacing it, and both answers lead to the same
// next step, with the real reply text preserved in the wrapped error.
func translateFTPError(err error) error {
	if err == nil {
		return nil
	}
	var te *textproto.Error
	if errors.As(err, &te) && (te.Code == 550 || te.Code == 450) {
		return fmt.Errorf("%w: %s", fs.ErrNotExist, te.Msg)
	}
	return err
}

var _ FS = (*ftpFS)(nil)
