package fsys

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"

	"goft/internal/config"
)

// sshLookup abstracts over the two ways ssh_config can be consulted: the
// library's own search path, or one file named by the job.
type sshLookup struct {
	get func(key string) (string, bool)
	all func(key string) []string
	// warnings explain default files that could not be read, so that their
	// settings not applying is said out loud.
	warnings []string
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
// failing to read it is an error: the job asked for it. Otherwise the default
// files are consulted in the order ssh uses, ~/.ssh/config and then
// /etc/ssh/ssh_config, the first to set a value winning.
//
// The default files are read one by one, so that one the parser rejects is
// skipped with a warning while the other still applies. Left to the library,
// a rejected ~/.ssh/config — and it rejects every Match criterion but host and
// all — silently took both files with it: a job relying on its Port or User
// connected somewhere else with no word as to why. Reading them here also
// finds the home directory the way the rest of goft does, through
// os.UserHomeDir, rather than through the account database.
func newSSHLookup(r config.Remote) (sshLookup, error) {
	if !r.SSHConfigEnabled() {
		return disabledLookup(), nil
	}

	if r.SSHConfigFile != "" {
		cfg, err := decodeSSHConfig(r.SSHConfigFile)
		if err != nil {
			return sshLookup{}, err
		}
		return configLookup(r.Host, []*ssh_config.Config{cfg}), nil
	}

	var (
		configs  []*ssh_config.Config
		warnings []string
	)
	for _, path := range defaultSSHConfigFiles() {
		cfg, err := decodeSSHConfig(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			warnings = append(warnings, fmt.Sprintf(
				"%v; none of its settings apply (only Match host and Match all are understood, so put what the job needs in the job file)", err))
			continue
		}
		configs = append(configs, cfg)
	}
	look := configLookup(r.Host, configs)
	look.warnings = warnings
	return look, nil
}

// defaultSSHConfigFiles lists the files ssh reads when none is named, most
// specific first.
func defaultSSHConfigFiles() []string {
	var files []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		files = append(files, filepath.Join(home, ".ssh", "config"))
	}
	return append(files, filepath.Join(string(filepath.Separator), "etc", "ssh", "ssh_config"))
}

func decodeSSHConfig(path string) (*ssh_config.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open ssh_config %s: %w", path, err)
	}
	defer f.Close()
	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("parse ssh_config %s: %w", path, err)
	}
	return cfg, nil
}

// configLookup answers from configs in order, the first to set a key winning,
// as ssh does across its files. A value counts as found precisely when a file
// sets it, so the source goft test reports is always the true one.
func configLookup(host string, configs []*ssh_config.Config) sshLookup {
	return sshLookup{
		get: func(key string) (string, bool) {
			for _, cfg := range configs {
				if v, err := cfg.Get(host, key); err == nil && v != "" {
					return v, true
				}
			}
			return "", false
		},
		all: func(key string) []string {
			for _, cfg := range configs {
				if v, err := cfg.GetAll(host, key); err == nil && len(v) > 0 {
					return v
				}
			}
			return nil
		},
	}
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
	res.Warnings = append(res.Warnings, look.warnings...)
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
//
// A key the job file names is always offered, so that failing to unlock it is
// reported rather than worked around. Keys from ssh_config or the default
// locations were not asked for by this job, so only those usable as they stand
// are offered, and the rest are skipped with a warning. Without that, a job
// authenticating by password could not connect from any account whose own
// ~/.ssh/id_ed25519 carries a passphrase, which is most accounts a person uses.
func resolveAuth(res *Resolved, r config.Remote, look sshLookup, home string) {
	if r.PrivateKey != "" {
		res.KeyFiles = []string{expandTokens(r.PrivateKey, res.Host, res.User, home)}
		res.record("private_key", res.KeyFiles[0], SourceYAML)
		return
	}

	var candidates []string
	source := SourceSSHConfig
	for _, k := range look.all("IdentityFile") {
		if p := expandTokens(k, res.Host, res.User, home); exists(p) {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 && home != "" {
		source = SourceDefault
		for _, name := range []string{"id_ed25519", "id_rsa"} {
			if p := filepath.Join(home, ".ssh", name); exists(p) {
				candidates = append(candidates, p)
			}
		}
	}

	for _, p := range candidates {
		if why := unusableKey(p, res.Passphrase); why != "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping the key %s: %s", p, why))
			continue
		}
		res.KeyFiles = append(res.KeyFiles, p)
	}
	if len(res.KeyFiles) > 0 {
		res.record("private_key", strings.Join(res.KeyFiles, ", "), source)
	}
}

// unusableKey says why a key file cannot be used as things stand, or returns
// an empty string when it can.
func unusableKey(path string, passphrase config.Secret) string {
	pem, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	_, err = parseKey(pem, passphrase)
	var locked *ssh.PassphraseMissingError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &locked):
		return "it is passphrase protected and private_key_passphrase is not set"
	default:
		return err.Error()
	}
}

// parseKey reads a private key, using the passphrase only if the key asks for
// one. A passphrase given for one key must not break another that has none.
func parseKey(pem []byte, passphrase config.Secret) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(pem)
	var locked *ssh.PassphraseMissingError
	if errors.As(err, &locked) && passphrase.IsSet() {
		return ssh.ParsePrivateKeyWithPassphrase(pem, []byte(string(passphrase)))
	}
	return signer, err
}

func resolveKnownHosts(res *Resolved, r config.Remote, look sshLookup, home string) {
	switch v, ok := look.get("UserKnownHostsFile"); {
	case r.KnownHosts != "":
		res.KnownHosts = expandTokens(r.KnownHosts, res.Host, res.User, home)
		res.record("known_hosts", res.KnownHosts, SourceYAML)
	case ok:
		// The directive may name several files; the first usable one wins.
		// When none exists yet the first is still the one meant, which is
		// where StrictHostKeyChecking accept-new records the first key and
		// what an error about a missing file should name.
		files := strings.Fields(v)
		for _, f := range files {
			if p := expandTokens(f, res.Host, res.User, home); exists(p) {
				res.KnownHosts = p
				break
			}
		}
		if res.KnownHosts == "" && len(files) > 0 {
			res.KnownHosts = expandTokens(files[0], res.Host, res.User, home)
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
//
// accept-new is not the same as no. It trusts a host it has never seen and
// records the key, but a host that is known and now presents a different key
// is still refused — which is what a man in the middle looks like, and the
// half of the setting that matters.
func resolveHostKeyPolicy(res *Resolved, look sshLookup) {
	v, ok := look.get("StrictHostKeyChecking")
	if !ok || res.SkipHostKey {
		return
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "no", "off":
		res.SkipHostKey = true
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("ssh_config sets StrictHostKeyChecking %s: the host key will not be verified", v))
	case "accept-new":
		res.AcceptNewHostKeys = true
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("ssh_config sets StrictHostKeyChecking %s: the key of a host not yet in %s will be accepted and recorded there; a changed key is still refused",
				v, res.KnownHosts))
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
