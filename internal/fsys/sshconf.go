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

	// HostName
	if v, ok := look.get("HostName"); ok {
		res.Host = v
		res.record("host", v, SourceSSHConfig)
	} else {
		res.record("host", r.Host, SourceYAML)
	}

	// Port
	switch v, ok := look.get("Port"); {
	case r.Port != 0:
		res.Port = r.Port
		res.record("port", strconv.Itoa(res.Port), SourceYAML)
	case ok:
		p, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("ssh_config Port %q: %w", v, err)
		}
		res.Port = p
		res.record("port", v, SourceSSHConfig)
	default:
		res.Port = 22
		res.record("port", "22", SourceDefault)
	}

	// User
	switch v, ok := look.get("User"); {
	case r.User != "":
		res.User = r.User
		res.record("user", res.User, SourceYAML)
	case ok:
		res.User = v
		res.record("user", v, SourceSSHConfig)
	default:
		if u, err := user.Current(); err == nil {
			res.User = u.Username
		}
		res.record("user", res.User, SourceDefault)
	}

	res.Password = r.Password
	if r.Password.IsSet() {
		res.record("password", r.Password.String(), SourceYAML)
	}
	res.Passphrase = r.PrivateKeyPassphrase

	// Identity files
	switch keys := look.all("IdentityFile"); {
	case r.PrivateKey != "":
		res.KeyFiles = []string{expandTokens(r.PrivateKey, res.Host, res.User, home)}
		res.record("private_key", res.KeyFiles[0], SourceYAML)
	case len(keys) > 0:
		for _, k := range keys {
			p := expandTokens(k, res.Host, res.User, home)
			if _, err := os.Stat(p); err == nil {
				res.KeyFiles = append(res.KeyFiles, p)
			}
		}
		if len(res.KeyFiles) > 0 {
			res.record("private_key", strings.Join(res.KeyFiles, ", "), SourceSSHConfig)
		}
	}
	if len(res.KeyFiles) == 0 && home != "" {
		for _, name := range []string{"id_ed25519", "id_rsa"} {
			p := filepath.Join(home, ".ssh", name)
			if _, err := os.Stat(p); err == nil {
				res.KeyFiles = append(res.KeyFiles, p)
			}
		}
		if len(res.KeyFiles) > 0 {
			res.record("private_key", strings.Join(res.KeyFiles, ", "), SourceDefault)
		}
	}

	// known_hosts
	switch v, ok := look.get("UserKnownHostsFile"); {
	case r.KnownHosts != "":
		res.KnownHosts = expandTokens(r.KnownHosts, res.Host, res.User, home)
		res.record("known_hosts", res.KnownHosts, SourceYAML)
	case ok:
		// The directive may name several files; the first usable one wins.
		for _, f := range strings.Fields(v) {
			p := expandTokens(f, res.Host, res.User, home)
			if _, err := os.Stat(p); err == nil {
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

	// StrictHostKeyChecking is honoured, because a job that names a host alias
	// is asking for that alias's settings. Relaxing verification is never
	// silent, though.
	if v, ok := look.get("StrictHostKeyChecking"); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "no", "off", "accept-new":
			if !res.SkipHostKey {
				res.SkipHostKey = true
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("ssh_config sets StrictHostKeyChecking %s: the host key will not be verified", v))
			}
		}
	}

	for _, key := range []string{"ProxyJump", "ProxyCommand"} {
		if v, ok := look.get(key); ok {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("ssh_config %s %q is not supported; connecting directly", key, v))
		}
	}

	return res, nil
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
