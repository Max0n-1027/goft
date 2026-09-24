package fsys

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"goft/internal/config"
	"goft/internal/sftptest"
)

// acceptNewRemote points a remote at an in-process server through an
// ssh_config that sets StrictHostKeyChecking accept-new and names knownHosts
// as the file to check against.
func acceptNewRemote(t *testing.T, knownHosts string) config.Remote {
	t.Helper()
	r := sftptest.Start(t, t.TempDir())
	cfg := writeSSHConfig(t, fmt.Sprintf(
		"Host %s\n  StrictHostKeyChecking accept-new\n  UserKnownHostsFile %s\n", r.Host, knownHosts))
	on := true
	r.UseSSHConfig = &on
	r.SSHConfigFile = cfg
	r.KnownHosts = ""
	return r
}

func TestAcceptNewIsNotTheSameAsNoChecking(t *testing.T) {
	cfgFile := writeSSHConfig(t, "Host invoice\n  StrictHostKeyChecking accept-new\n")
	res, err := resolveSFTP(remoteWithSSHConfig("invoice", cfgFile))
	if err != nil {
		t.Fatal(err)
	}
	// accept-new trusts a host it has never seen; it does not trust a host
	// whose key has changed. Treating it as "no" dropped the second half.
	if res.SkipHostKey {
		t.Error("accept-new must not turn host key verification off")
	}
	if !res.AcceptNewHostKeys {
		t.Error("accept-new should accept keys for hosts not yet known")
	}
	if len(res.Warnings) == 0 {
		t.Error("accepting unknown host keys must never be silent")
	}
}

func TestAcceptNewRecordsAnUnknownHost(t *testing.T) {
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := acceptNewRemote(t, knownHosts)

	fs, err := NewRemote(context.Background(), r)
	if err != nil {
		t.Fatalf("an unknown host should be accepted: %v", err)
	}
	fs.Close()

	// As OpenSSH does, the key is pinned: from now on this is a known host,
	// and a different key for it would be refused.
	b, err := os.ReadFile(knownHosts)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(b), "\n"); lines != 1 {
		t.Fatalf("known_hosts = %q, want the host key recorded once", b)
	}

	fs, err = NewRemote(context.Background(), r)
	if err != nil {
		t.Fatalf("the recorded key should verify the next connection: %v", err)
	}
	fs.Close()
	if b2, _ := os.ReadFile(knownHosts); string(b2) != string(b) {
		t.Errorf("a known host was recorded again: %q", b2)
	}
}

func TestAcceptNewCreatesTheKnownHostsFile(t *testing.T) {
	knownHosts := filepath.Join(t.TempDir(), "ssh", "known_hosts")
	r := acceptNewRemote(t, knownHosts)

	fs, err := NewRemote(context.Background(), r)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	fs.Close()
	if _, err := os.Stat(knownHosts); err != nil {
		t.Errorf("known_hosts should have been created: %v", err)
	}
}

func TestAcceptNewStillRefusesAChangedKey(t *testing.T) {
	// The whole point of accept-new over "no": a host that is known, but now
	// presents a different key, is exactly what a man in the middle looks like.
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	r := acceptNewRemote(t, knownHosts)

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	host := knownhosts.Normalize(fmt.Sprintf("%s:%d", r.Host, r.Port))
	if err := os.WriteFile(knownHosts, []byte(knownhosts.Line([]string{host}, other)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if fs, err := NewRemote(context.Background(), r); err == nil {
		fs.Close()
		t.Fatal("a changed host key was accepted")
	}
}

func TestAcceptNewRecordsAHostOnceWhenWorkersConnectTogether(t *testing.T) {
	// Every worker connects at the start of a cycle, so the first cycle meets
	// an unknown host several times at once.
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	r := acceptNewRemote(t, knownHosts)

	errs := make(chan error, 4)
	for range 4 {
		go func() {
			fs, err := NewRemote(context.Background(), r)
			if err == nil {
				fs.Close()
			}
			errs <- err
		}()
	}
	for range 4 {
		if err := <-errs; err != nil {
			t.Fatalf("connect: %v", err)
		}
	}

	b, err := os.ReadFile(knownHosts)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(b), "\n"); lines != 1 {
		t.Errorf("known_hosts has %d lines, want the host recorded once:\n%s", lines, b)
	}
}

func TestAHostKnownOnlyByItsEd25519KeyConnects(t *testing.T) {
	// The in-process server has an ECDSA host key as well as an ed25519 one,
	// and the known_hosts it hands out lists only the latter — the state
	// ssh leaves behind. Left to its own preference the client picked ECDSA
	// and reported a mismatch against a host ssh connects to without a word.
	r := sftptest.Start(t, t.TempDir())
	b, err := os.ReadFile(r.KnownHosts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), ssh.KeyAlgoED25519) || strings.Contains(string(b), "ecdsa") {
		t.Fatalf("known_hosts = %q, want the ed25519 key alone", b)
	}

	fs, err := NewRemote(context.Background(), r)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	fs.Close()
}

func TestHostKeyAlgorithmsFollowWhatIsKnown(t *testing.T) {
	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	edKey, _ := ssh.NewPublicKey(edPriv.Public())
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, _ := ssh.NewPublicKey(rsaPriv.Public())

	kh := filepath.Join(t.TempDir(), "known_hosts")
	lines := knownhosts.Line([]string{"known.example"}, edKey) + "\n" +
		knownhosts.Line([]string{"known.example"}, rsaKey) + "\n"
	if err := os.WriteFile(kh, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 22}

	got := knownHostAlgorithms(&Resolved{KnownHosts: kh}, "known.example:22", remote)
	want := []string{ssh.KeyAlgoED25519, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("algorithms = %v, want %v: only what known_hosts can vouch for", got, want)
	}

	// A host known_hosts does not list is left to the defaults, which is what
	// lets accept-new record whatever it is shown.
	if got := knownHostAlgorithms(&Resolved{KnownHosts: kh}, "unknown.example:22", remote); got != nil {
		t.Errorf("unknown host: algorithms = %v, want the defaults", got)
	}
	if got := knownHostAlgorithms(&Resolved{KnownHosts: kh, SkipHostKey: true}, "known.example:22", remote); got != nil {
		t.Errorf("no checking: algorithms = %v, want the defaults", got)
	}
}
