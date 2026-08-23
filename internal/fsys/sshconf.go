package fsys

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kevinburke/ssh_config"

	"goft/internal/config"
)

// sshLookup abstracts over the two ways ssh_config can be consulted: the
// library's own search path, or one file named by the job.
type sshLookup struct {
	get func(key string) (string, bool)
	all func(key string) []string
}

func disabledLookup() sshLookup {
	return sshLookup{
		get: func(string) (string, bool) { return "", false },
		all: func(string) []string { return nil },
	}
}

// newSSHLookup builds the accessor for a host alias.
//
// When the job names a file, that file replaces the search path entirely, and
// a value is "found" precisely when it appears there. When the default search
// path is used the library substitutes its own built-in defaults, so a value
// equal to the built-in default is attributed to `default` rather than to the
// file. The resolved value is the same either way; only the reported source can
// be off in that one case.
func newSSHLookup(r config.Remote) (sshLookup, error) {
	if !r.SSHConfigEnabled() {
		return disabledLookup(), nil
	}

	if r.SSHConfigFile != "" {
		f, err := os.Open(r.SSHConfigFile)
		if err != nil {
			return sshLookup{}, fmt.Errorf("open ssh_config %s: %w", r.SSHConfigFile, err)
		}
		defer f.Close()
		cfg, err := ssh_config.Decode(f)
		if err != nil {
			return sshLookup{}, fmt.Errorf("parse ssh_config %s: %w", r.SSHConfigFile, err)
		}
		return sshLookup{
			get: func(key string) (string, bool) {
				v, err := cfg.Get(r.Host, key)
				return v, err == nil && v != ""
			},
			all: func(key string) []string {
				v, err := cfg.GetAll(r.Host, key)
				if err != nil {
					return nil
				}
				return v
			},
		}, nil
	}

	us := &ssh_config.UserSettings{IgnoreErrors: true}
	return sshLookup{
		get: func(key string) (string, bool) {
			v := us.Get(r.Host, key)
			return v, v != "" && v != ssh_config.Default(key)
		},
		all: func(key string) []string { return us.GetAll(r.Host, key) },
	}, nil
}

// resolveSFTP merges the job configuration with ~/.ssh/config.
//
// Precedence is YAML, then ssh_config, then a built-in default. Every value
// records where it came from so that goft test and the debug log can explain
// the connection that was actually attempted.
func resolveSFTP(r config.Remote) (*Resolved, error) {
	look, err := newSSHLookup(r)
	if err != nil {
		return nil, err
	}

	res := &Resolved{Host: r.Host, SkipHostKey: r.InsecureSkipHostKeyCheck}
	home, _ := os.UserHomeDir()

	resolveHost(res, r, look)
	if err := resolvePort(res, r, look); err != nil {
		return nil, err
	}
	resolveCredentials(res, r)
	resolveUser(res, look)
	resolveAuth(res, r, look, home)
	resolveKnownHosts(res, r, look, home)
	resolveHostKeyPolicy(res, look)

	for _, key := range []string{"ProxyJump", "ProxyCommand"} {
		if v, ok := look.get(key); ok {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("ssh_config %s %q is not supported; connecting directly", key, v))
		}
	}

	return res, nil
}

func resolveHost(res *Resolved, r config.Remote, look sshLookup) {
	if v, ok := look.get("HostName"); ok {
		res.Host = v
		res.record("host", v, SourceSSHConfig)
		return
	}
	res.record("host", r.Host, SourceYAML)
}

func resolvePort(res *Resolved, r config.Remote, look sshLookup) error {
	// A port of zero is not a port, so a non-zero value is exactly the same
	// test as "was it configured".
	if r.Port != 0 {
		res.Port = r.Port
		res.record("port", strconv.Itoa(res.Port), SourceYAML)
		return nil
	}
	if v, ok := look.get("Port"); ok {
		p, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("ssh_config Port %q: %w", v, err)
		}
		res.Port = p
		res.record("port", v, SourceSSHConfig)
		return nil
	}
	res.Port = 22
	res.record("port", "22", SourceDefault)
	return nil
}

// resolveCredentials settles the user and password before ssh_config is read.
//
// The credential store sits between the job file and the default files, so it
// has to be consulted here rather than alongside the identity files: by the
// time ssh_config has supplied a User, or the current login has been taken as
// the default, there is no gap left for a stored user name to fill.
func resolveCredentials(res *Resolved, r config.Remote) {
	if r.User != "" {
		res.User = r.User
		res.record("user", r.User, SourceYAML)
	}
	res.Password = r.Password
	if r.Password.IsSet() {
		res.record("password", r.Password.String(), SourceYAML)
	}
	// The store holds a password, never a key passphrase: a passphrase unlocks
	// a file rather than authenticating to a host, and has no user name to be
	// registered against.
	res.Passphrase = r.PrivateKeyPassphrase

	applyCredential(res, r)
}

func resolveUser(res *Resolved, look sshLookup) {
	if res.User != "" {
		return
	}
	if v, ok := look.get("User"); ok {
		res.User = v
		res.record("user", v, SourceSSHConfig)
		return
	}
	if u, err := user.Current(); err == nil {
		res.User = u.Username
	}
	res.record("user", res.User, SourceDefault)
}

// resolveAuth settles the identities to offer.
//
// Identity files are filtered by existence, because ssh_config commonly names
// several and only some of them are on any given machine.
func resolveAuth(res *Resolved, r config.Remote, look sshLookup, home string) {
	switch keys := look.all("IdentityFile"); {
	case r.PrivateKey != "":
		res.KeyFiles = []string{expandTokens(r.PrivateKey, res.Host, res.User, home)}
		res.record("private_key", res.KeyFiles[0], SourceYAML)
		return
	case len(keys) > 0:
		for _, k := range keys {
			if p := expandTokens(k, res.Host, res.User, home); exists(p) {
				res.KeyFiles = append(res.KeyFiles, p)
			}
		}
		if len(res.KeyFiles) > 0 {
			res.record("private_key", strings.Join(res.KeyFiles, ", "), SourceSSHConfig)
			return
		}
	}

	if home == "" {
		return
	}
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		if p := filepath.Join(home, ".ssh", name); exists(p) {
			res.KeyFiles = append(res.KeyFiles, p)
		}
	}
	if len(res.KeyFiles) > 0 {
		res.record("private_key", strings.Join(res.KeyFiles, ", "), SourceDefault)
	}
}

func resolveKnownHosts(res *Resolved, r config.Remote, look sshLookup, home string) {
	switch v, ok := look.get("UserKnownHostsFile"); {
	case r.KnownHosts != "":
		res.KnownHosts = expandTokens(r.KnownHosts, res.Host, res.User, home)
		res.record("known_hosts", res.KnownHosts, SourceYAML)
	case ok:
		// The directive may name several files; the first usable one wins.
		for _, f := range strings.Fields(v) {
			if p := expandTokens(f, res.Host, res.User, home); exists(p) {
				res.KnownHosts = p
				break
			}
		}
		if res.KnownHosts != "" {
			res.record("known_hosts", res.KnownHosts, SourceSSHConfig)
		}
	default:
		if home != "" {
			res.KnownHosts = filepath.Join(home, ".ssh", "known_hosts")
			res.record("known_hosts", res.KnownHosts, SourceDefault)
		}
	}
}

// resolveHostKeyPolicy honours the alias's own setting, because a job naming an
// alias is asking for that alias's configuration. Relaxing verification is
// never silent.
func resolveHostKeyPolicy(res *Resolved, look sshLookup) {
	v, ok := look.get("StrictHostKeyChecking")
	if !ok || res.SkipHostKey {
		return
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "no", "off", "accept-new":
		res.SkipHostKey = true
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("ssh_config sets StrictHostKeyChecking %s: the host key will not be verified", v))
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// expandTokens resolves the parts of an ssh_config path that the parser leaves
// alone: a leading ~ and the %h, %u and %d tokens.
func expandTokens(s, host, user, home string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "~/") && home != "" {
		s = filepath.Join(home, s[2:])
	}
	r := strings.NewReplacer("%h", host, "%u", user, "%d", home, "%%", "%")
	return r.Replace(s)
}
