package fsys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"goft/internal/config"
)

// writeKey writes a new ed25519 private key, encrypted when passphrase is set.
func writeKey(t *testing.T, path, passphrase string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	saveKey(t, path, priv, passphrase)
}

// saveKey writes priv in the OpenSSH format, encrypted when passphrase is set.
func saveKey(t *testing.T, path string, priv ed25519.PrivateKey, passphrase string) {
	t.Helper()
	var (
		block *pem.Block
		err   error
	)
	if passphrase != "" {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	} else {
		block, err = ssh.MarshalPrivateKey(priv, "")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
}

// withHome points the home directory at a fresh one holding the given default
// key, on every platform.
func withHome(t *testing.T, passphrase string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeKey(t, filepath.Join(home, ".ssh", "id_ed25519"), passphrase)
	return home
}

func passwordRemote() config.Remote {
	off := false
	return config.Remote{
		Protocol: config.ProtocolSFTP, Host: "h", Path: "/p",
		User: "u", Password: config.Secret("pw"), UseSSHConfig: &off,
	}
}

func TestAPassphraseProtectedDefaultKeyDoesNotBlockAPassword(t *testing.T) {
	// A person's own account usually has a passphrase on ~/.ssh/id_ed25519.
	// A job that authenticates by password never asked for that key, and must
	// not fail to connect because it cannot be unlocked.
	withHome(t, "secret phrase")

	res, err := resolveSFTP(passwordRemote())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.KeyFiles) != 0 {
		t.Errorf("key files = %v, want the locked default key left out", res.KeyFiles)
	}
	if !warned(res, "passphrase") {
		t.Errorf("warnings = %v, want the skipped key explained", res.Warnings)
	}
	auths, err := sftpAuths(res, nil)
	if err != nil || len(auths) != 1 {
		t.Fatalf("sftpAuths() = %d methods, %v; want the password on its own", len(auths), err)
	}
}

func TestADefaultKeyIsUsedWhenItsPassphraseIsGiven(t *testing.T) {
	withHome(t, "secret phrase")
	r := passwordRemote()
	r.PrivateKeyPassphrase = config.Secret("secret phrase")

	res, err := resolveSFTP(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.KeyFiles) != 1 {
		t.Fatalf("key files = %v, want the default key kept", res.KeyFiles)
	}
	if auths, err := sftpAuths(res, nil); err != nil || len(auths) != 2 {
		t.Errorf("sftpAuths() = %d methods, %v; want the key and the password", len(auths), err)
	}
}

func TestAnUnencryptedDefaultKeyIsStillOffered(t *testing.T) {
	withHome(t, "")
	res, err := resolveSFTP(passwordRemote())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.KeyFiles) != 1 || len(res.Warnings) != 0 {
		t.Errorf("key files = %v, warnings = %v; want the key offered quietly", res.KeyFiles, res.Warnings)
	}
}

func TestALockedIdentityFileFromSSHConfigIsSkippedToo(t *testing.T) {
	home := withHome(t, "")
	locked := filepath.Join(home, ".ssh", "deploy")
	writeKey(t, locked, "secret phrase")
	cfg := writeSSHConfig(t, "Host h\n  IdentityFile "+locked+"\n")

	r := passwordRemote()
	on := true
	r.UseSSHConfig, r.SSHConfigFile = &on, cfg

	res, err := resolveSFTP(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range res.KeyFiles {
		if k == locked {
			t.Errorf("key files = %v, want %s left out", res.KeyFiles, locked)
		}
	}
	if !warned(res, locked) {
		t.Errorf("warnings = %v, want the skipped key named", res.Warnings)
	}
}

func TestAConfiguredKeyThatCannotBeUnlockedIsAnError(t *testing.T) {
	// private_key in the job file is a deliberate choice, so failing to unlock
	// it is reported rather than quietly worked around.
	home := withHome(t, "")
	locked := filepath.Join(home, ".ssh", "deploy")
	writeKey(t, locked, "secret phrase")
	r := passwordRemote()
	r.PrivateKey = locked

	res, err := resolveSFTP(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sftpAuths(res, nil); err == nil {
		t.Error("want an error for a configured key that cannot be unlocked")
	}
}

func warned(res *Resolved, substr string) bool {
	for _, w := range res.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestAPassphraseDoesNotBreakAKeyThatHasNone(t *testing.T) {
	// private_key_passphrase is meant for the key that needs it. An unencrypted
	// key alongside used to be parsed with it too, and rejected for not being
	// encrypted.
	withHome(t, "")
	r := passwordRemote()
	r.PrivateKeyPassphrase = config.Secret("meant for another key")

	res, err := resolveSFTP(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.KeyFiles) != 1 {
		t.Fatalf("key files = %v, want the unencrypted key kept", res.KeyFiles)
	}
	if auths, err := sftpAuths(res, nil); err != nil || len(auths) != 2 {
		t.Errorf("sftpAuths() = %d methods, %v; want the key and the password", len(auths), err)
	}
}
