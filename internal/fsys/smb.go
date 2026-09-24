package fsys

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	smb2 "github.com/cloudsoda/go-smb2"

	"goft/internal/config"
)

type smbFS struct {
	root    string
	desc    string
	session *smb2.Session
	share   *smb2.Share
	conn    net.Conn
}

// resolveSMB reports the connection parameters for an SMB share.
//
// SMB has no credential file of its own to fall back on. The ones Windows keeps
// for network shares are domain credentials, whose password the platform
// reserves for the authentication packages, so they cannot be reused however
// convenient that would be. Credentials come from the job configuration, from
// ${VAR} expansion, or from a generic Credential Manager entry registered for
// goft.
func resolveSMB(r config.Remote) (*Resolved, error) {
	res := &Resolved{Host: r.Host, Port: r.Port, User: r.User, Password: r.Password}
	res.record("host", r.Host, SourceYAML)
	if r.Port != 0 {
		res.record("port", strconv.Itoa(r.Port), SourceYAML)
	} else {
		res.Port = 445
		res.record("port", "445", SourceDefault)
	}
	if r.User != "" {
		res.record("user", r.User, SourceYAML)
	}
	if r.Password.IsSet() {
		res.record("password", r.Password.String(), SourceYAML)
	}
	applyCredential(res, r)
	res.record("share", r.Share, SourceYAML)
	if r.Domain != "" {
		res.record("domain", r.Domain, SourceYAML)
	}
	return res, nil
}

func newSMB(ctx context.Context, r config.Remote) (FS, error) {
	res, err := resolveSMB(r)
	if err != nil {
		return nil, err
	}
	// Checked here rather than during validation, because a Credential Manager
	// entry may supply them and that is only known once resolved.
	if res.User == "" || !res.Password.IsSet() {
		return nil, fmt.Errorf("no smb credentials for %s: set remote.user and remote.password, or register %q with cmdkey", r.Host, credentialTarget(r))
	}

	addr := net.JoinHostPort(res.Host, strconv.Itoa(res.Port))
	// The TCP connection, negotiation, authentication and mounting share one
	// deadline, lifted once the share is usable. A server that accepted the
	// connection and then never answered used to hold the job there for good.
	timeout := r.ConnectTimeoutOrDefault()
	deadline := time.Now().Add(timeout)
	d := net.Dialer{Deadline: deadline}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, connectErr(err, timeout, deadline))
	}
	_ = conn.SetDeadline(deadline)

	dialer := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     res.User,
			Password: string(res.Password),
			Domain:   r.Domain,
		},
	}
	session, err := dialer.DialConn(ctx, conn, addr)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smb session with %s: %w", addr, connectErr(err, timeout, deadline))
	}

	// Paths are share relative from here on, which is why remote.path must not
	// repeat the share name.
	share, err := session.Mount(r.Share)
	if err != nil {
		_ = session.Logoff()
		conn.Close()
		return nil, fmt.Errorf("mount share %q on %s: %w", r.Share, addr, connectErr(err, timeout, deadline))
	}
	_ = conn.SetDeadline(time.Time{})

	return &smbFS{
		root:    strings.TrimPrefix(r.Path, "/"),
		desc:    fmt.Sprintf("smb://%s/%s/%s", addr, r.Share, strings.TrimPrefix(r.Path, "/")),
		session: session,
		// The share keeps this context for every call it makes. A stop
		// request cancels the one the connection was opened with, and a
		// transfer under way is meant to finish, as it does on the other
		// protocols; a stalled one is dropped by io_timeout instead.
		share: share.WithContext(context.WithoutCancel(ctx)),
		conn:  conn,
	}, nil
}

func (s *smbFS) abs(name string) string {
	joined := path.Join(s.root, name)
	return strings.TrimPrefix(joined, "/")
}

// Describe implements FS.
func (s *smbFS) Describe() string { return s.desc }

// CaseInsensitive implements FS. SMB shares fold case, so an existence check
// has to fold it too.
func (s *smbFS) CaseInsensitive() bool { return true }

// List implements FS.
func (s *smbFS) List(_ context.Context, dir string) ([]Entry, error) {
	infos, err := s.share.ReadDir(s.abs(dir))
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		out = append(out, Entry{
			Name:      fi.Name(),
			Size:      fi.Size(),
			ModTime:   fi.ModTime(),
			IsDir:     fi.IsDir(),
			IsRegular: fi.Mode().IsRegular(),
		})
	}
	return out, nil
}

// Stat implements FS.
func (s *smbFS) Stat(_ context.Context, name string) (FileInfo, error) {
	fi, err := s.share.Stat(s.abs(name))
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Size: fi.Size(), ModTime: fi.ModTime()}, nil
}

// Open implements FS.
func (s *smbFS) Open(_ context.Context, name string) (io.ReadCloser, error) {
	return s.share.Open(s.abs(name))
}

// Write implements FS.
func (s *smbFS) Write(_ context.Context, name string, r io.Reader) (int64, error) {
	f, err := s.share.OpenFile(s.abs(name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, transferredFileMode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if err != nil {
		f.Close()
		return n, err
	}
	return n, f.Close()
}

// MkdirAll implements FS. The share only creates one level at a time.
//
// The root is included. It used to be created only on the way to a
// subdirectory, so a destination that did not exist yet failed on the first
// transfer whenever the files went straight into it — despite goft test having
// said it would be created.
func (s *smbFS) MkdirAll(_ context.Context, dir string) error {
	return s.mkdirAll(s.abs(dir))
}

// mkdirAll creates p, and its parents only if they turn out to be missing, so
// that the usual case — a new directory in one that exists — costs one round
// trip rather than one per level of the path.
//
// A level Mkdir refuses is fine only if a directory is what holds that name.
// "Already exists" is also the answer for a file in the way, and taking that
// for success sent the job on to a write that failed with the reason gone.
func (s *smbFS) mkdirAll(p string) error {
	if p == "" || p == "." {
		return nil // the share itself
	}
	err := s.share.Mkdir(p, 0o755)
	if err == nil {
		return nil
	}
	if fi, statErr := s.share.Stat(p); statErr == nil {
		if fi.IsDir() {
			return nil
		}
		return fmt.Errorf("%s is a file, not a directory: %w", p, fs.ErrExist)
	}
	if parent := path.Dir(p); parent != "." && parent != p {
		if perr := s.mkdirAll(parent); perr != nil {
			return perr
		}
		if err = s.share.Mkdir(p, 0o755); err == nil {
			return nil
		}
	}
	return err
}

// Rename implements FS.
func (s *smbFS) Rename(_ context.Context, from, to string) error {
	return s.share.Rename(s.abs(from), s.abs(to))
}

// Remove implements FS.
func (s *smbFS) Remove(_ context.Context, name string) error {
	return s.share.Remove(s.abs(name))
}

// abort drops the connection under whatever is waiting on it.
func (s *smbFS) abort() { _ = s.conn.Close() }

// Close implements FS.
func (s *smbFS) Close() error {
	err := s.share.Umount()
	if lerr := s.session.Logoff(); err == nil {
		err = lerr
	}
	if cerr := s.conn.Close(); err == nil {
		err = cerr
	}
	return err
}

var _ FS = (*smbFS)(nil)
