package fsys

import (
	"context"
	"fmt"
	"path/filepath"

	"goft/internal/config"
)

// NewRemote opens a connection to the configured remote side.
//
// Each call returns an independent connection: the engine gives every worker
// its own, which is why the live connection count never exceeds workers.
func NewRemote(ctx context.Context, r config.Remote) (FS, error) {
	switch r.Protocol {
	case config.ProtocolSFTP:
		return newSFTP(ctx, r)
	case config.ProtocolFTP:
		return newFTP(ctx, r)
	case config.ProtocolSMB:
		return newSMB(ctx, r)
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
	// Source is where it came from: yaml, ssh_config, netrc or default.
	Source string
}

// Sources for a resolved connection parameter.
const (
	SourceYAML      = "yaml"
	SourceSSHConfig = "ssh_config"
	SourceNetrc     = "netrc"
	SourceDefault   = "default"
)

// Resolved is the outcome of merging YAML with ssh_config or netrc.
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
	// KnownHosts verifies the host key unless SkipHostKey is set.
	KnownHosts  string
	SkipHostKey bool
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
