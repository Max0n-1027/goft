package fsys

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"goft/internal/config"
)

type sftpFS struct {
	root   string
	desc   string
	client *sftp.Client
	conn   *ssh.Client
}

func newSFTP(ctx context.Context, r config.Remote) (FS, error) {
	res, err := resolveSFTP(r)
	if err != nil {
		return nil, err
	}

	auths, err := sftpAuths(res)
	if err != nil {
		return nil, err
	}
	if len(auths) == 0 {
		return nil, errors.New("no sftp authentication available: set remote.private_key or remote.password, or provide an IdentityFile in ssh_config")
	}

	hostKey, err := hostKeyCallback(res)
	if err != nil {
		return nil, err
	}

	addr := net.JoinHostPort(res.Host, strconv.Itoa(res.Port))
	d := net.Dialer{Timeout: 30 * time.Second}
	netConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, addr, &ssh.ClientConfig{
		User:            res.User,
		Auth:            auths,
		HostKeyCallback: hostKey,
		Timeout:         30 * time.Second,
	})
	if err != nil {
		netConn.Close()
		return nil, fmt.Errorf("ssh handshake with %s: %w", addr, err)
	}
	conn := ssh.NewClient(sshConn, chans, reqs)

	client, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("start sftp subsystem on %s: %w", addr, err)
	}

	return &sftpFS{
		root:   r.Path,
		desc:   fmt.Sprintf("sftp://%s%s", addr, r.Path),
		client: client,
		conn:   conn,
	}, nil
}

func sftpAuths(res *Resolved) ([]ssh.AuthMethod, error) {
	var auths []ssh.AuthMethod
	var signers []ssh.Signer

	for _, keyPath := range res.KeyFiles {
		pem, err := os.ReadFile(keyPath)
		if err != nil {
			// A key named by ssh_config but unreadable is not fatal on its own;
			// another key or a password may still work.
			continue
		}
		signer, err := parseKey(pem, res.Passphrase)
		if err != nil {
			return nil, fmt.Errorf("parse private key %s: %w", keyPath, err)
		}
		signers = append(signers, signer)
	}
	if len(signers) > 0 {
		auths = append(auths, ssh.PublicKeys(signers...))
	}
	if res.Password.IsSet() {
		auths = append(auths, ssh.Password(string(res.Password)))
	}
	return auths, nil
}

func hostKeyCallback(res *Resolved) (ssh.HostKeyCallback, error) {
	if res.SkipHostKey {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	if res.KnownHosts == "" {
		return nil, errors.New("no known_hosts file to verify the host key against; set remote.known_hosts or remote.insecure_skip_host_key_check")
	}
	if res.AcceptNewHostKeys {
		if err := ensureKnownHosts(res.KnownHosts); err != nil {
			return nil, err
		}
	}
	cb, err := knownhosts.New(res.KnownHosts)
	if err != nil {
		return nil, fmt.Errorf("read known_hosts %s: %w", res.KnownHosts, err)
	}
	if !res.AcceptNewHostKeys {
		return cb, nil
	}

	path := res.KnownHosts
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := cb(hostname, remote, key)
		if !isUnknownHost(err) {
			// Verified, or refused: a known host with a different key is
			// refused here exactly as it would be without accept-new.
			return err
		}
		return recordHostKey(path, hostname, remote, key)
	}, nil
}

// isUnknownHost reports whether a known_hosts check failed only because the
// host is not listed at all, as opposed to being listed with another key.
func isUnknownHost(err error) bool {
	var ke *knownhosts.KeyError
	return errors.As(err, &ke) && len(ke.Want) == 0
}

// knownHostsMu serialises additions to known_hosts. Every worker connects at
// the start of a cycle, so several can meet the same unknown host at once.
var knownHostsMu sync.Mutex

// recordHostKey pins the key of a host seen for the first time, as OpenSSH's
// accept-new does, so that from the next connection on it is a known host and a
// different key for it is refused.
//
// The file is read again under the lock first: another worker may have recorded
// this host a moment ago, and if it recorded a different key than this
// connection is being shown, that is a conflict to refuse rather than a second
// line to add.
func recordHostKey(path, hostname string, remote net.Addr, key ssh.PublicKey) error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()

	cb, err := knownhosts.New(path)
	if err != nil {
		return fmt.Errorf("read known_hosts %s: %w", path, err)
	}
	if err := cb(hostname, remote, key); !isUnknownHost(err) {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("record host key in %s: %w", path, err)
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return fmt.Errorf("record host key in %s: %w", path, err)
	}
	return f.Close()
}

// ensureKnownHosts creates an empty known_hosts, and its directory, when accept-new
// is to record the first key in a file that does not exist yet.
func ensureKnownHosts(path string) error {
	if exists(path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create known_hosts %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("create known_hosts %s: %w", path, err)
	}
	return f.Close()
}

func (s *sftpFS) abs(name string) string {
	if name == "" {
		return s.root
	}
	return path.Join(s.root, name)
}

// Describe implements FS.
func (s *sftpFS) Describe() string { return s.desc }

// CaseInsensitive implements FS. SFTP servers are POSIX in practice.
func (s *sftpFS) CaseInsensitive() bool { return false }

// List implements FS.
func (s *sftpFS) List(_ context.Context, dir string) ([]Entry, error) {
	infos, err := s.client.ReadDir(s.abs(dir))
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
func (s *sftpFS) Stat(_ context.Context, name string) (FileInfo, error) {
	fi, err := s.client.Stat(s.abs(name))
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Size: fi.Size(), ModTime: fi.ModTime()}, nil
}

// Open implements FS.
func (s *sftpFS) Open(_ context.Context, name string) (io.ReadCloser, error) {
	return s.client.Open(s.abs(name))
}

// Write implements FS. sftp.Create truncates an existing file.
func (s *sftpFS) Write(_ context.Context, name string, r io.Reader) (int64, error) {
	f, err := s.client.Create(s.abs(name))
	if err != nil {
		return 0, err
	}
	n, err := f.ReadFrom(r)
	if err != nil {
		f.Close()
		return n, err
	}
	return n, f.Close()
}

// MkdirAll implements FS.
func (s *sftpFS) MkdirAll(_ context.Context, dir string) error {
	return s.client.MkdirAll(s.abs(dir))
}

// Rename implements FS.
//
// PosixRename replaces an existing target atomically, so a file being published
// over an older one is never briefly absent. Servers without the extension fall
// back to the plain rename, which the caller handles by clearing the name.
func (s *sftpFS) Rename(_ context.Context, from, to string) error {
	if err := s.client.PosixRename(s.abs(from), s.abs(to)); err == nil {
		return nil
	}
	return s.client.Rename(s.abs(from), s.abs(to))
}

// Remove implements FS.
func (s *sftpFS) Remove(_ context.Context, name string) error {
	return s.client.Remove(s.abs(name))
}

// Close implements FS.
func (s *sftpFS) Close() error {
	err := s.client.Close()
	if cerr := s.conn.Close(); err == nil {
		err = cerr
	}
	return err
}

var _ FS = (*sftpFS)(nil)
