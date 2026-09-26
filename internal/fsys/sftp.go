package fsys

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
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
	// Everything up to a usable connection shares one deadline, the agent and
	// the TCP connection included. A dial timeout alone covers only the
	// latter, and a server that accepts it and then never starts the handshake
	// held the job there for good.
	timeout := r.ConnectTimeoutOrDefault()
	deadline := time.Now().Add(timeout)

	res, err := resolveSFTPBy(r, deadline)
	if err != nil {
		return nil, err
	}

	agentSigners, closeAgent, err := openAgent(res.Agent, deadline)
	if err != nil {
		return nil, err
	}
	defer closeAgent()

	auths, err := sftpAuths(res, agentSigners)
	if err != nil {
		return nil, err
	}
	if len(auths) == 0 {
		return nil, errors.New("no sftp authentication available: set remote.private_key or remote.password, provide an IdentityFile in ssh_config, or load a key into ssh-agent")
	}

	hostKey, err := hostKeyCallback(res)
	if err != nil {
		return nil, err
	}

	addr := net.JoinHostPort(res.Host, strconv.Itoa(res.Port))
	d := net.Dialer{Deadline: deadline}
	netConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, connectErr(err, timeout, deadline))
	}
	_ = netConn.SetDeadline(deadline)

	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, addr, &ssh.ClientConfig{
		User:              res.User,
		Auth:              auths,
		HostKeyCallback:   hostKey,
		HostKeyAlgorithms: knownHostAlgorithms(res, addr, netConn.RemoteAddr()),
	})
	if err != nil {
		netConn.Close()
		return nil, fmt.Errorf("ssh handshake with %s: %w", addr, connectErr(err, timeout, deadline))
	}
	conn := ssh.NewClient(sshConn, chans, reqs)

	client, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("start sftp subsystem on %s: %w", addr, connectErr(err, timeout, deadline))
	}
	_ = netConn.SetDeadline(time.Time{})

	return &sftpFS{
		root:   r.Path,
		desc:   fmt.Sprintf("sftp://%s%s", addr, r.Path),
		client: client,
		conn:   conn,
	}, nil
}

// sftpAuths lists the ways to authenticate, keys before a password as ssh
// tries them.
func sftpAuths(res *Resolved, agentSigners []ssh.Signer) ([]ssh.AuthMethod, error) {
	var auths []ssh.AuthMethod
	signers, err := offeredSigners(res, agentSigners)
	if err != nil {
		return nil, err
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

// hostKeyPreference is the order host key algorithms are offered in when
// known_hosts restricts them, strongest first, as OpenSSH orders them. Each is
// listed with the type of key it is a signature over.
var hostKeyPreference = []struct{ algorithm, keyType string }{
	{ssh.KeyAlgoED25519, ssh.KeyAlgoED25519},
	{ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA256},
	{ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA384},
	{ssh.KeyAlgoECDSA521, ssh.KeyAlgoECDSA521},
	{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSA},
	{ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA},
	// Still offered for a known RSA key that has nothing better, as it was
	// before; a server limited to it keeps working. A host known only by a
	// DSA key matches nothing here and is left to the defaults, as before.
	{ssh.KeyAlgoRSA, ssh.KeyAlgoRSA},
}

// probeKey is offered to the known_hosts check only to learn which keys it holds
// for a host; no host will ever present it.
var probeKey = sync.OnceValue(func() ssh.PublicKey {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil
	}
	return key
})

// knownHostAlgorithms lists the host key algorithms to negotiate: those
// known_hosts holds a key for, for this host.
//
// Left to itself the client offers ECDSA before ed25519, and a server with
// both keys — a stock OpenSSH server — then presents its ECDSA key. The
// known_hosts OpenSSH leaves behind often lists only the ed25519 one, having
// negotiated that, so the check found a key of a different type and reported a
// mismatch: a host that ssh connected to without complaint was refused as if
// under attack. OpenSSH avoids this by preferring the algorithms it already
// has a key for, and so does this.
//
// A host known_hosts does not list, or a check that is not being made, leaves
// the choice to the defaults.
func knownHostAlgorithms(res *Resolved, addr string, remote net.Addr) []string {
	if res.SkipHostKey || res.KnownHosts == "" || probeKey() == nil {
		return nil
	}
	check, err := knownhosts.New(res.KnownHosts)
	if err != nil {
		return nil
	}
	var ke *knownhosts.KeyError
	if err := check(addr, remote, probeKey()); !errors.As(err, &ke) || len(ke.Want) == 0 {
		return nil
	}

	known := map[string]bool{}
	for _, k := range ke.Want {
		known[k.Key.Type()] = true
	}
	var algorithms []string
	for _, p := range hostKeyPreference {
		if known[p.keyType] {
			algorithms = append(algorithms, p.algorithm)
		}
	}
	return algorithms
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

// abort drops the connection under whatever is waiting on it.
func (s *sftpFS) abort() { _ = s.conn.Close() }

// Close implements FS.
func (s *sftpFS) Close() error {
	err := s.client.Close()
	if cerr := s.conn.Close(); err == nil {
		err = cerr
	}
	return err
}

var _ FS = (*sftpFS)(nil)
