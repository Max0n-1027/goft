package fsys

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"goft/internal/config"
)

// NewRemote opens a connection to the configured remote side.
//
// Each call returns an independent connection: the engine gives every worker
// its own, which is why the live connection count never exceeds workers.
//
// A connection that stops moving data for remote.io_timeout is dropped, so that
// a call waiting on it returns with [ErrStalled] rather than never.
func NewRemote(ctx context.Context, r config.Remote) (FS, error) {
	var (
		fs  FS
		err error
	)
	switch r.Protocol {
	case config.ProtocolSFTP:
		fs, err = newSFTP(ctx, r)
	case config.ProtocolFTP:
		fs, err = newFTP(ctx, r)
	case config.ProtocolSMB:
		fs, err = newSMB(ctx, r)
	default:
		return newNonNetwork(r)
	}
	if err != nil {
		return nil, err
	}
	return guard(fs, r.IOTimeout), nil
}

// newNonNetwork opens what is not reached over a connection, which is also
// nothing a stall could be caught on.
func newNonNetwork(r config.Remote) (FS, error) {
	switch r.Protocol {
	case config.ProtocolLocal:
		// Nothing is opened: the far side is a directory on this machine, and
		// the engine cannot tell the difference.
		return NewLocal(r.Path), nil
	default:
		return nil, fmt.Errorf("unsupported protocol %q", r.Protocol)
	}
}

// Resolution records where a connection parameter came from, so that goft test
// and the debug log can explain a surprising setting.
type Resolution struct {
	// Field is the setting, such as "port" or "user".
	Field string
	// Value is what it ended up as, with any secret already masked.
	Value string
	// Source is where it came from: yaml, ssh_config, netrc,
	// credential_manager, env or default.
	Source string
}

// Sources for a resolved connection parameter.
const (
	SourceYAML      = "yaml"
	SourceSSHConfig = "ssh_config"
	SourceNetrc     = "netrc"
	SourceDefault   = "default"
)

// Resolved is the outcome of merging the job configuration with whatever the
// protocol consults besides: ssh_config, netrc, the Credential Manager or the
// ssh-agent.
type Resolved struct {
	// Host is the name to connect to, which for an sftp alias is the HostName
	// from ssh_config rather than the alias itself.
	Host string
	Port int
	// User, Password and Passphrase authenticate the connection.
	User       string
	Password   config.Secret
	Passphrase config.Secret
	// KeyFiles are the sftp identities to offer, in order.
	KeyFiles []string
	// Agent is the ssh-agent to take keys from, a Unix socket or on Windows a
	// named pipe, or empty for none. IdentitiesOnly limits it to the keys in
	// KeyFiles, as ssh_config's IdentitiesOnly does and as naming private_key
	// in the job does.
	Agent          string
	IdentitiesOnly bool
	// KnownHosts verifies the host key unless SkipHostKey is set.
	KnownHosts  string
	SkipHostKey bool
	// AcceptNewHostKeys trusts and records the key of a host KnownHosts does
	// not list yet, while still refusing a host whose key has changed. It is
	// what ssh_config's StrictHostKeyChecking accept-new asks for.
	AcceptNewHostKeys bool
	// Trace records where each value above came from, which is what goft test
	// prints and the debug log records.
	Trace []Resolution
	// Warnings are conditions worth telling the operator about, such as an
	// ssh_config directive that goft cannot honour.
	Warnings []string
}

// Resolve merges the job configuration with whatever default files the
// protocol consults, without opening a connection. goft test uses it to show
// what a connection attempt would actually use.
func Resolve(r config.Remote) (*Resolved, error) {
	switch r.Protocol {
	case config.ProtocolSFTP:
		return resolveSFTP(r)
	case config.ProtocolFTP:
		return resolveFTP(r)
	case config.ProtocolSMB:
		return resolveSMB(r)
	case config.ProtocolLocal:
		return resolveLocal(r)
	default:
		return nil, fmt.Errorf("unsupported protocol %q", r.Protocol)
	}
}

// connectErr says plainly that a connection ran out of time, rather than
// leaving the operator to read "i/o timeout" off whichever read happened to be
// waiting when the deadline passed.
//
// Whether it did is judged by the clock as well as by the error: not every
// library keeps the deadline error it was handed — go-smb2 turns it into text —
// and a failure at or after the deadline is the deadline's doing either way.
func connectErr(err error, timeout time.Duration, deadline time.Time) error {
	if errors.Is(err, os.ErrDeadlineExceeded) || !time.Now().Before(deadline) {
		return fmt.Errorf("no usable connection within connect_timeout %v: %w", timeout, err)
	}
	return err
}

// resolveLocal reports the far side of a local copy. There is nothing to look
// up — no host, no port, no credentials — so the path is all there is to show,
// and goft test shows it rather than printing nothing at all.
func resolveLocal(r config.Remote) (*Resolved, error) {
	res := &Resolved{}
	path, err := filepath.Abs(r.Path)
	if err != nil {
		return nil, fmt.Errorf("remote.path %s: %w", r.Path, err)
	}
	res.record("path", path, SourceYAML)
	return res, nil
}

func (r *Resolved) record(field, value, source string) {
	r.Trace = append(r.Trace, Resolution{Field: field, Value: value, Source: source})
}
