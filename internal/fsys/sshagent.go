package fsys

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"goft/internal/config"
)

// SourceEnv marks a connection parameter taken from an environment variable,
// such as the ssh-agent socket in SSH_AUTH_SOCK.
const SourceEnv = "env"

// agentConn is the connection to an ssh-agent: a Unix socket, or on Windows a
// named pipe. Both can be given a deadline, so an agent that stops answering
// holds a connection attempt no longer than connect_timeout.
type agentConn interface {
	io.ReadWriteCloser
	SetDeadline(t time.Time) error
}

// dialAgentSocket connects to an agent listening on a Unix socket, which is how
// agents listen everywhere but, usually, Windows.
func dialAgentSocket(addr string, deadline time.Time) (agentConn, error) {
	d := net.Dialer{Deadline: deadline}
	conn, err := d.Dial("unix", addr)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(deadline)
	return conn, nil
}

// agentKeys is the set of public keys an agent holds, keyed by their wire form.
type agentKeys map[string]bool

func (k agentKeys) holds(pub ssh.PublicKey) bool {
	return pub != nil && k[string(pub.Marshal())]
}

// agentAddress finds the agent to ask, as ssh does: the job's own setting, then
// IdentityAgent in ssh_config, then SSH_AUTH_SOCK, then — on Windows only —
// the pipe the OpenSSH Authentication Agent service listens on.
//
// An empty address means no agent. Off says it was turned off on purpose,
// which goft test shows, rather than simply never found.
func agentAddress(r config.Remote, look sshLookup, res *Resolved, home string) (addr, source string, off bool) {
	if !r.SSHAgentEnabled() {
		return "", SourceYAML, true
	}
	if r.SSHAgent != "" {
		return expandTokens(r.SSHAgent, res.Host, res.User, home), SourceYAML, false
	}
	if v, ok := look.get("IdentityAgent"); ok {
		switch {
		case strings.EqualFold(v, "none"):
			return "", SourceSSHConfig, true
		case v == "SSH_AUTH_SOCK":
			return os.Getenv("SSH_AUTH_SOCK"), SourceSSHConfig, false
		case strings.HasPrefix(v, "$"):
			// A value starting with $ names the variable holding the socket.
			return os.Getenv(v[1:]), SourceSSHConfig, false
		}
		return expandTokens(v, res.Host, res.User, home), SourceSSHConfig, false
	}
	if v := os.Getenv("SSH_AUTH_SOCK"); v != "" {
		return v, SourceEnv, false
	}
	return defaultAgent, SourceDefault, false
}

// resolveAgent settles the ssh-agent to take keys from, and returns the keys it
// holds so that resolveAuth can count a passphrase protected key file the agent
// already has as usable, along with where the agent's address came from.
//
// The agent is asked for its keys now, before any connection is made, so that
// goft test and the start of the log can say whether it was reachable and what
// it had to offer. An agent the job or the environment points at but that
// cannot be reached is a warning, not an error: a password or a key file may
// still get the job in. Nothing listening where an agent usually is, though,
// is how most Windows machines are, and is not worth a word.
func resolveAgent(res *Resolved, r config.Remote, look sshLookup, home string, deadline time.Time) (agentKeys, string) {
	addr, source, off := agentAddress(r, look, res, home)
	switch {
	case off:
		value := "off"
		if source == SourceSSHConfig {
			value = "none"
		}
		res.record("ssh_agent", value, source)
		return nil, source
	case addr == "":
		return nil, source
	}

	keys, err := listAgentKeys(addr, deadline)
	if err != nil {
		if source != SourceDefault {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("the ssh-agent at %s cannot be used, so none of its keys are offered: %v", addr, err))
		}
		return nil, source
	}
	res.Agent = addr
	return keys, source
}

// recordAgent notes the agent in use and how many of its keys will be offered,
// which with IdentitiesOnly may be fewer than it holds.
func recordAgent(res *Resolved, keys agentKeys, source string) {
	if res.Agent == "" {
		return
	}
	offered := len(keys)
	if res.IdentitiesOnly {
		matched := make(agentKeys)
		for _, path := range res.KeyFiles {
			if pem, err := os.ReadFile(path); err == nil {
				if pub := publicHalf(path, pem); keys.holds(pub) {
					matched[string(pub.Marshal())] = true
				}
			}
		}
		offered = len(matched)
	}
	value := fmt.Sprintf("%s (holds %s)", res.Agent, countKeys(len(keys)))
	if offered != len(keys) {
		value = fmt.Sprintf("%s (offers %d of the %s it holds)", res.Agent, offered, countKeys(len(keys)))
	}
	res.record("ssh_agent", value, source)
}

func countKeys(n int) string {
	if n == 1 {
		return "1 key"
	}
	return fmt.Sprintf("%d keys", n)
}

// listAgentKeys asks the agent at addr which keys it holds.
func listAgentKeys(addr string, deadline time.Time) (agentKeys, error) {
	conn, err := dialAgent(addr, deadline)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	list, err := agent.NewClient(conn).List()
	if err != nil {
		return nil, err
	}
	keys := make(agentKeys, len(list))
	for _, k := range list {
		keys[string(k.Marshal())] = true
	}
	return keys, nil
}

// openAgent connects to the agent resolution settled on and returns its keys,
// ready to sign with. The agent signs during the handshake, so the connection
// has to stay open until that is over; the function returned closes it.
func openAgent(addr string, deadline time.Time) ([]ssh.Signer, func(), error) {
	if addr == "" {
		return nil, func() {}, nil
	}
	conn, err := dialAgent(addr, deadline)
	if err != nil {
		return nil, nil, fmt.Errorf("ssh-agent %s: %w", addr, err)
	}
	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("ssh-agent %s: %w", addr, err)
	}
	return signers, func() { _ = conn.Close() }, nil
}

// publicHalf returns the public key of a private key file without needing its
// passphrase, or nil when that cannot be done. A key in the OpenSSH format
// carries it in the clear; for a key in the older PEM format it is read from the
// .pub file beside it, which is where ssh-keygen leaves it.
func publicHalf(path string, pem []byte) ssh.PublicKey {
	signer, err := ssh.ParsePrivateKey(pem)
	if err == nil {
		return signer.PublicKey()
	}
	var locked *ssh.PassphraseMissingError
	if errors.As(err, &locked) && locked.PublicKey != nil {
		return locked.PublicKey
	}
	line, err := os.ReadFile(path + ".pub")
	if err != nil {
		return nil
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(line)
	if err != nil {
		return nil
	}
	return pub
}

// offeredSigners puts the keys to offer in the order OpenSSH offers them: the
// agent's copy of each key file it holds, then the agent's other keys, then the
// key files it does not hold.
//
// Taking a key file's key from the agent is what lets a passphrase protected
// key be used without its passphrase. With IdentitiesOnly the agent's other
// keys are left out, so a server that counts every key it is offered against
// its limit sees only the ones the job was meant to use.
func offeredSigners(res *Resolved, agentSigners []ssh.Signer) ([]ssh.Signer, error) {
	held := make(map[string]ssh.Signer, len(agentSigners))
	for _, s := range agentSigners {
		held[string(s.PublicKey().Marshal())] = s
	}

	var fromAgent, fromFiles []ssh.Signer
	used := make(map[string]bool)
	for _, path := range res.KeyFiles {
		pem, err := os.ReadFile(path)
		if err != nil {
			// A key named by ssh_config but unreadable is not fatal on its own;
			// another key or a password may still work.
			continue
		}
		if pub := publicHalf(path, pem); pub != nil {
			wire := string(pub.Marshal())
			if s, ok := held[wire]; ok {
				if !used[wire] {
					fromAgent = append(fromAgent, s)
					used[wire] = true
				}
				continue
			}
		}
		signer, err := parseKey(pem, res.Passphrase)
		if err != nil {
			return nil, fmt.Errorf("parse private key %s: %w", path, err)
		}
		fromFiles = append(fromFiles, signer)
	}

	if !res.IdentitiesOnly {
		for _, s := range agentSigners {
			if wire := string(s.PublicKey().Marshal()); !used[wire] {
				fromAgent = append(fromAgent, s)
				used[wire] = true
			}
		}
	}
	return append(fromAgent, fromFiles...), nil
}
