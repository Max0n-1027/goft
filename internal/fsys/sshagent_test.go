package fsys

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"goft/internal/config"
	"goft/internal/sftptest"
)

func newKey(t *testing.T) (ed25519.PrivateKey, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return priv, sshPub
}

// emptyHome points the home directory somewhere with no keys in it, so the
// only keys offered are the ones a test sets up.
func emptyHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// keyOnlyServer is a server that lets in only key, and a job with no password
// to fall back on.
func keyOnlyServer(t *testing.T, key ssh.PublicKey) config.Remote {
	t.Helper()
	r := sftptest.Start(t, t.TempDir(), key)
	r.Password = ""
	return r
}

func connect(r config.Remote) error {
	f, err := NewRemote(context.Background(), r)
	if err != nil {
		return err
	}
	return f.Close()
}

func traceValue(res *Resolved, field string) (Resolution, bool) {
	for _, t := range res.Trace {
		if t.Field == field {
			return t, true
		}
	}
	return Resolution{}, false
}

func TestTheAgentsKeyAuthenticates(t *testing.T) {
	emptyHome(t)
	priv, pub := newKey(t)
	r := keyOnlyServer(t, pub)
	sock := sftptest.StartAgent(t, priv)
	t.Setenv("SSH_AUTH_SOCK", sock)

	if err := connect(r); err != nil {
		t.Fatalf("connect with the key in the agent: %v", err)
	}

	res, err := Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := traceValue(res, "ssh_agent")
	if !ok || got.Value != sock+" (holds 1 key)" || got.Source != SourceEnv {
		t.Errorf("ssh_agent trace = %+v, want %s (holds 1 key) from %s", got, sock, SourceEnv)
	}
}

func TestTheAgentCanBeTurnedOff(t *testing.T) {
	emptyHome(t)
	priv, pub := newKey(t)
	r := keyOnlyServer(t, pub)
	t.Setenv("SSH_AUTH_SOCK", sftptest.StartAgent(t, priv))
	off := false
	r.UseSSHAgent = &off

	err := connect(r)
	if err == nil || !strings.Contains(err.Error(), "no sftp authentication available") {
		t.Fatalf("connect = %v, want no way to authenticate once the agent is off", err)
	}

	res, err := Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := traceValue(res, "ssh_agent"); got.Value != "off" || got.Source != SourceYAML {
		t.Errorf("ssh_agent trace = %+v, want off from %s", got, SourceYAML)
	}
}

func TestTheAgentIsFoundAsSSHFindsIt(t *testing.T) {
	home := t.TempDir()
	saved := defaultAgent
	defaultAgent = "/default"
	t.Cleanup(func() { defaultAgent = saved })
	off := false

	tests := []struct {
		name       string
		job        config.Remote
		sshConfig  map[string]string
		env        string
		wantAddr   string
		wantSource string
		wantOff    bool
	}{
		{
			name:       "turned off in the job",
			job:        config.Remote{UseSSHAgent: &off, SSHAgent: "/job"},
			sshConfig:  map[string]string{"IdentityAgent": "/cfg"},
			env:        "/env",
			wantSource: SourceYAML,
			wantOff:    true,
		},
		{
			name:       "named in the job",
			job:        config.Remote{SSHAgent: "~/job.sock"},
			sshConfig:  map[string]string{"IdentityAgent": "/cfg"},
			env:        "/env",
			wantAddr:   filepath.Join(home, "job.sock"),
			wantSource: SourceYAML,
		},
		{
			name:       "IdentityAgent over the environment",
			sshConfig:  map[string]string{"IdentityAgent": "~/%u.sock"},
			env:        "/env",
			wantAddr:   filepath.Join(home, "tester.sock"),
			wantSource: SourceSSHConfig,
		},
		{
			name:       "IdentityAgent none",
			sshConfig:  map[string]string{"IdentityAgent": "none"},
			env:        "/env",
			wantSource: SourceSSHConfig,
			wantOff:    true,
		},
		{
			name:       "IdentityAgent SSH_AUTH_SOCK",
			sshConfig:  map[string]string{"IdentityAgent": "SSH_AUTH_SOCK"},
			env:        "/env",
			wantAddr:   "/env",
			wantSource: SourceSSHConfig,
		},
		{
			name:       "IdentityAgent naming a variable",
			sshConfig:  map[string]string{"IdentityAgent": "$GOFT_TEST_AGENT"},
			env:        "/env",
			wantAddr:   "/named-by-variable",
			wantSource: SourceSSHConfig,
		},
		{
			name:       "the environment",
			env:        "/env",
			wantAddr:   "/env",
			wantSource: SourceEnv,
		},
		{
			name:       "nowhere",
			wantAddr:   "/default",
			wantSource: SourceDefault,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SSH_AUTH_SOCK", tt.env)
			t.Setenv("GOFT_TEST_AGENT", "/named-by-variable")
			look := sshLookup{
				get: func(key string) (string, bool) {
					v, ok := tt.sshConfig[key]
					return v, ok
				},
				all: func(string) []string { return nil },
			}
			res := &Resolved{Host: "h", User: "tester"}

			addr, source, off := agentAddress(tt.job, look, res, home)
			if addr != tt.wantAddr || source != tt.wantSource || off != tt.wantOff {
				t.Errorf("agentAddress() = %q, %s, off %v; want %q, %s, off %v",
					addr, source, off, tt.wantAddr, tt.wantSource, tt.wantOff)
			}
		})
	}
}

func TestIdentityAgentNoneIsShown(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", sftptest.StartAgent(t))
	cfg := writeSSHConfig(t, "Host h\n  IdentityAgent none\n")
	res, err := resolveSFTP(remoteWithSSHConfig("h", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := traceValue(res, "ssh_agent"); got.Value != "none" || got.Source != SourceSSHConfig || res.Agent != "" {
		t.Errorf("ssh_agent trace = %+v, agent %q; want none from %s and no agent", got, res.Agent, SourceSSHConfig)
	}
}

func TestAPassphraseProtectedKeyTheAgentHoldsNeedsNoPassphrase(t *testing.T) {
	emptyHome(t)
	priv, pub := newKey(t)
	keyFile := filepath.Join(t.TempDir(), "id")
	saveKey(t, keyFile, priv, "a passphrase")
	r := keyOnlyServer(t, pub)
	r.PrivateKey = keyFile

	off := false
	r.UseSSHAgent = &off
	if err := connect(r); err == nil || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("connect without the agent = %v, want the locked key reported", err)
	}

	r.UseSSHAgent = nil
	t.Setenv("SSH_AUTH_SOCK", sftptest.StartAgent(t, priv))
	if err := connect(r); err != nil {
		t.Fatalf("connect with the key in the agent: %v", err)
	}
}

func TestNamingAPrivateKeyLimitsTheAgentToIt(t *testing.T) {
	emptyHome(t)
	agentPriv, agentPub := newKey(t)
	filePriv, _ := newKey(t)
	keyFile := filepath.Join(t.TempDir(), "id")
	saveKey(t, keyFile, filePriv, "")
	r := keyOnlyServer(t, agentPub)
	sock := sftptest.StartAgent(t, agentPriv)
	t.Setenv("SSH_AUTH_SOCK", sock)

	if err := connect(r); err != nil {
		t.Fatalf("connect with nothing but the agent: %v", err)
	}

	r.PrivateKey = keyFile
	if err := connect(r); err == nil {
		t.Fatal("connected with the agent's key although the job named another")
	}
	res, err := Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := traceValue(res, "ssh_agent"); got.Value != sock+" (offers 0 of the 1 key it holds)" {
		t.Errorf("ssh_agent trace = %q, want it to say the agent's key is not offered", got.Value)
	}
}

func TestIdentitiesOnlyLimitsTheAgent(t *testing.T) {
	emptyHome(t)
	agentPriv, agentPub := newKey(t)
	filePriv, _ := newKey(t)
	keyFile := filepath.Join(t.TempDir(), "id")
	saveKey(t, keyFile, filePriv, "")
	r := keyOnlyServer(t, agentPub)
	t.Setenv("SSH_AUTH_SOCK", sftptest.StartAgent(t, agentPriv))
	on := true
	r.UseSSHConfig = &on

	r.SSHConfigFile = writeSSHConfig(t, "Host 127.0.0.1\n  IdentityFile "+keyFile+"\n")
	if err := connect(r); err != nil {
		t.Fatalf("connect with the agent's key after the file's: %v", err)
	}

	r.SSHConfigFile = writeSSHConfig(t, "Host 127.0.0.1\n  IdentityFile "+keyFile+"\n  IdentitiesOnly yes\n")
	if err := connect(r); err == nil {
		t.Fatal("connected with the agent's key although IdentitiesOnly is set")
	}
	res, err := Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := traceValue(res, "identities_only"); !ok || got.Source != SourceSSHConfig {
		t.Errorf("identities_only trace = %+v, want it shown from %s", got, SourceSSHConfig)
	}
}

func TestAPassphraseProtectedDefaultKeyTheAgentHoldsIsOffered(t *testing.T) {
	home := withHome(t, "a passphrase")
	keyFile := filepath.Join(home, ".ssh", "id_ed25519")
	data, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ssh.ParseRawPrivateKeyWithPassphrase(data, []byte("a passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	r := keyOnlyServer(t, signer.PublicKey())
	t.Setenv("SSH_AUTH_SOCK", sftptest.StartAgent(t, priv))

	res, err := resolveSFTP(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.KeyFiles) != 1 || len(res.Warnings) != 0 {
		t.Errorf("key files = %v, warnings = %v; want the key offered quietly", res.KeyFiles, res.Warnings)
	}
	if err := connect(r); err != nil {
		t.Fatalf("connect with the default key through the agent: %v", err)
	}
}

func TestASkippedKeySaysTheAgentDoesNotHoldIt(t *testing.T) {
	withHome(t, "a passphrase")
	t.Setenv("SSH_AUTH_SOCK", sftptest.StartAgent(t))

	res, err := resolveSFTP(passwordRemote())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.KeyFiles) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "ssh-agent does not hold it") {
		t.Errorf("key files = %v, warnings = %v; want the key skipped, naming the agent", res.KeyFiles, res.Warnings)
	}
}

func TestAnAgentThatCannotBeReachedIsAWarning(t *testing.T) {
	emptyHome(t)
	gone := filepath.Join(t.TempDir(), "gone")
	t.Setenv("SSH_AUTH_SOCK", gone)
	r := sftptest.Start(t, t.TempDir())

	res, err := resolveSFTP(r)
	if err != nil {
		t.Fatal(err)
	}
	if res.Agent != "" || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], gone) {
		t.Errorf("agent = %q, warnings = %v; want no agent, and one warning naming %s", res.Agent, res.Warnings, gone)
	}
	if err := connect(r); err != nil {
		t.Fatalf("the password must still get in: %v", err)
	}
}

func TestNoAgentWhereOneUsuallyIsSaysNothing(t *testing.T) {
	saved := defaultAgent
	defaultAgent = filepath.Join(t.TempDir(), "gone")
	t.Cleanup(func() { defaultAgent = saved })

	res, err := resolveSFTP(passwordRemote())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := traceValue(res, "ssh_agent"); ok || len(res.Warnings) != 0 {
		t.Errorf("trace = %v, warnings = %v; want nothing said about an agent that was never set up", res.Trace, res.Warnings)
	}
}

// silentAgent listens where an agent would, accepts connections, and never
// answers, as an agent that has hung does.
func silentAgent(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(sftptest.ShortTempDir(t), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	return sock
}

func TestAnAgentThatStopsAnsweringIsGivenUpOn(t *testing.T) {
	sock := silentAgent(t)
	var err error

	r := passwordRemote()
	r.ConnectTimeout = 200 * time.Millisecond
	t.Setenv("SSH_AUTH_SOCK", sock)
	var res *Resolved
	finishesWithin(t, 5*time.Second, "resolving against an agent that never answers", func() {
		res, err = resolveSFTP(r)
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Agent != "" || len(res.Warnings) != 1 {
		t.Errorf("agent = %q, warnings = %v; want the silent agent given up on with a warning", res.Agent, res.Warnings)
	}

	finishesWithin(t, 5*time.Second, "opening an agent that never answers", func() {
		_, _, err = openAgent(sock, time.Now().Add(200*time.Millisecond))
	})
	if err == nil {
		t.Error("openAgent() on an agent that never answers = nil error, want it to time out")
	}
}

// finishesWithin runs f, failing the test at once if it has not returned by limit
// rather than leaving it to hang until the whole run times out.
func finishesWithin(t *testing.T, limit time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("%s did not return within %v", what, limit)
	}
}

func TestOpenAgentReportsAnAgentThatHasGone(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	if _, _, err := openAgent(gone, time.Now().Add(time.Second)); err == nil || !strings.Contains(err.Error(), gone) {
		t.Errorf("openAgent() = %v, want an error naming %s", err, gone)
	}
	signers, closeAgent, err := openAgent("", time.Now())
	if err != nil || signers != nil {
		t.Errorf("openAgent(\"\") = %v, %v; want nothing to do", signers, err)
	}
	closeAgent()
}

func TestKeysAreOfferedInTheOrderSSHOffersThem(t *testing.T) {
	dir := t.TempDir()
	heldPriv, _ := newKey(t)
	filePriv, filePub := newKey(t)
	agentOnlyPriv, _ := newKey(t)
	held := filepath.Join(dir, "held")
	saveKey(t, held, heldPriv, "")
	fileOnly := filepath.Join(dir, "file")
	saveKey(t, fileOnly, filePriv, "")

	keyring := agent.NewKeyring()
	for _, k := range []any{agentOnlyPriv, heldPriv} {
		if err := keyring.Add(agent.AddedKey{PrivateKey: k}); err != nil {
			t.Fatal(err)
		}
	}
	agentSigners, err := keyring.Signers()
	if err != nil {
		t.Fatal(err)
	}
	agentHeld, agentOnly := agentSigners[1], agentSigners[0]
	if string(agentHeld.PublicKey().Marshal()) != string(mustPublic(t, heldPriv).Marshal()) {
		agentHeld, agentOnly = agentOnly, agentHeld
	}

	res := &Resolved{KeyFiles: []string{fileOnly, filepath.Join(dir, "missing"), held}}
	got, err := offeredSigners(res, agentSigners)
	if err != nil {
		t.Fatal(err)
	}
	// The agent's copy of a key file, then the agent's other keys, then the
	// files the agent does not hold.
	if len(got) != 3 || got[0] != agentHeld || got[1] != agentOnly ||
		string(got[2].PublicKey().Marshal()) != string(filePub.Marshal()) {
		t.Errorf("offered %d keys in the wrong order", len(got))
	}

	res.IdentitiesOnly = true
	got, err = offeredSigners(res, agentSigners)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != agentHeld || string(got[1].PublicKey().Marshal()) != string(filePub.Marshal()) {
		t.Errorf("offered %d keys with IdentitiesOnly, want the held key and the file only", len(got))
	}
}

func mustPublic(t *testing.T, priv ed25519.PrivateKey) ssh.PublicKey {
	t.Helper()
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestALockedKeyTheAgentDoesNotHoldIsStillAnError(t *testing.T) {
	priv, _ := newKey(t)
	keyFile := filepath.Join(t.TempDir(), "id")
	saveKey(t, keyFile, priv, "a passphrase")
	otherPriv, _ := newKey(t)
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: otherPriv}); err != nil {
		t.Fatal(err)
	}
	signers, err := keyring.Signers()
	if err != nil {
		t.Fatal(err)
	}

	_, err = offeredSigners(&Resolved{KeyFiles: []string{keyFile}}, signers)
	if err == nil || !strings.Contains(err.Error(), keyFile) {
		t.Errorf("offeredSigners() = %v, want the locked key named", err)
	}
}

// writeLegacyKey writes an ECDSA key in the older PEM format, whose encryption
// hides the public key as well as the private one.
func writeLegacyKey(t *testing.T, path, passphrase string) ssh.PublicKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	//lint:ignore SA1019 the point is to write a key in the format that is deprecated.
	block, err := x509.EncryptPEMBlock(rand.Reader, "EC PRIVATE KEY", der, []byte(passphrase), x509.PEMCipherAES256) //nolint:staticcheck
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestThePublicHalfOfAKeyIsFoundWithoutItsPassphrase(t *testing.T) {
	dir := t.TempDir()

	plainPriv, plainPub := newKey(t)
	plain := filepath.Join(dir, "plain")
	saveKey(t, plain, plainPriv, "")

	lockedPriv, lockedPub := newKey(t)
	locked := filepath.Join(dir, "locked")
	saveKey(t, locked, lockedPriv, "a passphrase")

	legacy := filepath.Join(dir, "legacy")
	legacyPub := writeLegacyKey(t, legacy, "a passphrase")
	legacyAlone := filepath.Join(dir, "legacy-alone")
	writeLegacyKey(t, legacyAlone, "a passphrase")
	if err := os.WriteFile(legacy+".pub", ssh.MarshalAuthorizedKey(legacyPub), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken")
	writeLegacyKey(t, broken, "a passphrase")
	if err := os.WriteFile(broken+".pub", []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		want ssh.PublicKey
	}{
		{"unencrypted", plain, plainPub},
		{"encrypted in the OpenSSH format", locked, lockedPub},
		{"encrypted PEM with its .pub", legacy, legacyPub},
		{"encrypted PEM on its own", legacyAlone, nil},
		{"encrypted PEM with a broken .pub", broken, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			got := publicHalf(tt.path, data)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("publicHalf() = %s, want nil", ssh.FingerprintSHA256(got))
			case tt.want != nil && (got == nil || string(got.Marshal()) != string(tt.want.Marshal())):
				t.Errorf("publicHalf() = %v, want %s", got, ssh.FingerprintSHA256(tt.want))
			}
		})
	}
}

func TestAnAgentThatStopsAnsweringCountsAgainstConnectTimeout(t *testing.T) {
	// The agent is asked for its keys as part of connecting, so the time spent
	// waiting on it comes out of connect_timeout rather than being added to it.
	r := sftptest.Start(t, t.TempDir())
	r.ConnectTimeout = 300 * time.Millisecond
	t.Setenv("SSH_AUTH_SOCK", silentAgent(t))

	var err error
	finishesWithin(t, 5*time.Second, "connecting with an agent that never answers", func() {
		err = connect(r)
	})
	if err == nil || !strings.Contains(err.Error(), "connect_timeout") {
		t.Errorf("connect = %v, want the attempt to run out of connect_timeout", err)
	}
}
