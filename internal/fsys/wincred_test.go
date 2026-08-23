package fsys

import (
	"strings"
	"testing"

	"goft/internal/config"
)

func TestDecodeSecretHandlesBothEncodings(t *testing.T) {
	utf16le := func(s string) []byte {
		var b []byte
		for _, r := range s {
			b = append(b, byte(r), byte(r>>8))
		}
		return b
	}

	for _, tc := range []struct {
		name string
		blob []byte
		want string
	}{
		// cmdkey and the Credential Manager window write wide strings.
		{"cmdkey", utf16le("s3cret"), "s3cret"},
		{"cmdkey with punctuation", utf16le("p@ss w0rd!"), "p@ss w0rd!"},
		// Tools ported from elsewhere write UTF-8.
		{"utf-8", []byte("s3cret"), "s3cret"},
		{"utf-8 of even length", []byte("secret"), "secret"},
		{"utf-8 multibyte", []byte("パスワード"), "パスワード"},
		{"empty", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := decodeSecret(tc.blob); got != tc.want {
				t.Errorf("decodeSecret(%v) = %q, want %q", tc.blob, got, tc.want)
			}
		})
	}
}

func TestCredentialTarget(t *testing.T) {
	// Microsoft's guidance for a generic credential is to prefix the name with
	// the program, which also keeps goft off another tool's entry.
	got := credentialTarget(config.Remote{Protocol: config.ProtocolSMB, Host: "fileserver"})
	if got != "goft:smb://fileserver" {
		t.Errorf("credentialTarget() = %q, want goft:smb://fileserver", got)
	}

	// An existing registration can be named instead.
	custom := credentialTarget(config.Remote{
		Protocol: config.ProtocolSMB, Host: "fileserver", CredentialTarget: "LegacyEntry",
	})
	if custom != "LegacyEntry" {
		t.Errorf("credentialTarget() = %q, want the configured name", custom)
	}
}

func TestCredentialLookupIsInertWhereThereIsNoStore(t *testing.T) {
	// Everywhere but Windows the call is unsupported, so resolution has to
	// carry on exactly as it did before.
	_, _, found := lookupCredential(config.Remote{Protocol: config.ProtocolSMB, Host: "h"})
	if found {
		t.Skip("a credential store answered; this must be Windows")
	}

	res := &Resolved{}
	applyCredential(res, config.Remote{Protocol: config.ProtocolSMB, Host: "h"})
	if res.User != "" || res.Password.IsSet() || len(res.Trace) != 0 {
		t.Errorf("resolution was touched with no store present: %+v", res)
	}
}

func TestCredentialManagerCanBeTurnedOff(t *testing.T) {
	off := false
	if _, _, found := lookupCredential(config.Remote{
		Protocol: config.ProtocolSMB, Host: "h", UseCredentialManager: &off,
	}); found {
		t.Error("use_credential_manager: false must stop the lookup entirely")
	}
}

func TestConfiguredCredentialsWinOverTheStore(t *testing.T) {
	// The store is a place to keep a password out of the file, not a way to
	// override one that is in it.
	res := &Resolved{User: "fromyaml", Password: config.Secret("fromyaml")}
	applyCredential(res, config.Remote{Protocol: config.ProtocolSMB, Host: "h"})

	if res.User != "fromyaml" || string(res.Password) != "fromyaml" {
		t.Errorf("resolution = %q/%v, want the configuration untouched", res.User, res.Password.IsSet())
	}
	for _, tr := range res.Trace {
		if strings.Contains(tr.Source, SourceCredentialManager) {
			t.Errorf("the store overrode a configured value: %+v", tr)
		}
	}
}

// stubCredentialStore stands in for the Windows store, which cannot be reached
// from a test on any other platform. It keeps the one decision the real lookup
// makes before touching the store, so use_credential_manager: false is still
// exercised through the same path.
func stubCredentialStore(t *testing.T, user, password string) {
	t.Helper()
	prev := credentialStore
	credentialStore = func(r config.Remote) (string, config.Secret, bool) {
		if !r.CredentialManagerEnabled() {
			return "", "", false
		}
		return user, config.Secret(password), true
	}
	t.Cleanup(func() { credentialStore = prev })
}

func TestCredentialTargetNamesTheProtocol(t *testing.T) {
	// One host may serve more than one protocol, so the entry for an FTP job
	// has to be distinguishable from the one for a share on the same machine.
	got := credentialTarget(config.Remote{Protocol: config.ProtocolFTP, Host: "ftp.example.com"})
	if got != "goft:ftp://ftp.example.com" {
		t.Errorf("credentialTarget() = %q, want goft:ftp://ftp.example.com", got)
	}
}

func TestResolveFTPFillsCredentialsFromTheStore(t *testing.T) {
	stubCredentialStore(t, "svc-transfer", "fromstore")
	p := writeNetrc(t, "machine ftp.example.com login alice password fromnetrc\n", 0o600)

	res, err := resolveFTP(config.Remote{
		Protocol:  config.ProtocolFTP,
		Host:      "ftp.example.com",
		NetrcFile: p,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The store is consulted first, so a job with an entry registered for it
	// need not keep a netrc at all.
	if res.User != "svc-transfer" || string(res.Password) != "fromstore" {
		t.Errorf("resolved user=%q, want the credentials from the store", res.User)
	}
	if !tracedFrom(res, "user", SourceCredentialManager) {
		t.Errorf("trace = %+v, want the user attributed to the store", res.Trace)
	}
	for _, tr := range res.Trace {
		if strings.Contains(tr.Value, "fromstore") {
			t.Fatalf("trace entry %+v leaks the password", tr)
		}
	}
}

func TestResolveFTPPrefersTheJobConfigurationOverTheStore(t *testing.T) {
	stubCredentialStore(t, "svc-transfer", "fromstore")

	res, err := resolveFTP(config.Remote{
		Protocol: config.ProtocolFTP,
		Host:     "ftp.example.com",
		User:     "bob",
		Password: config.Secret("fromyaml"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.User != "bob" || string(res.Password) != "fromyaml" {
		t.Errorf("resolved user=%q, want the explicit configuration to win", res.User)
	}
}

func TestResolveFTPCompletesStoreCredentialsFromNetrc(t *testing.T) {
	// An entry registered with a password but no user name is worth using for
	// the half it does hold; the rest still comes from the file.
	stubCredentialStore(t, "", "fromstore")
	p := writeNetrc(t, "machine ftp.example.com login alice password fromnetrc\n", 0o600)

	res, err := resolveFTP(config.Remote{
		Protocol:  config.ProtocolFTP,
		Host:      "ftp.example.com",
		NetrcFile: p,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.User != "alice" || string(res.Password) != "fromstore" {
		t.Errorf("resolved user=%q password=from the store? %v, want alice with the stored password",
			res.User, string(res.Password) == "fromstore")
	}
}

func TestResolveFTPFallsBackToNetrcWhenTheStoreIsOff(t *testing.T) {
	stubCredentialStore(t, "svc-transfer", "fromstore")
	p := writeNetrc(t, "machine ftp.example.com login alice password fromnetrc\n", 0o600)
	off := false

	res, err := resolveFTP(config.Remote{
		Protocol:             config.ProtocolFTP,
		Host:                 "ftp.example.com",
		NetrcFile:            p,
		UseCredentialManager: &off,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.User != "alice" || string(res.Password) != "fromnetrc" {
		t.Errorf("resolved user=%q, want the netrc credentials once the store is turned off", res.User)
	}
}

func TestResolveSFTPFillsCredentialsFromTheStore(t *testing.T) {
	stubCredentialStore(t, "svc-transfer", "fromstore")
	// A user name in ssh_config must not shadow the one registered for goft:
	// the store sits between the job file and the default files.
	p := writeSSHConfig(t, "Host sftp.example.com\n  User fromsshconfig\n")

	res, err := resolveSFTP(remoteWithSSHConfig("sftp.example.com", p))
	if err != nil {
		t.Fatal(err)
	}
	if res.User != "svc-transfer" || string(res.Password) != "fromstore" {
		t.Errorf("resolved user=%q, want the credentials from the store", res.User)
	}
	if !tracedFrom(res, "user", SourceCredentialManager) {
		t.Errorf("trace = %+v, want the user attributed to the store", res.Trace)
	}
}

func TestResolveSFTPPrefersTheJobConfigurationOverTheStore(t *testing.T) {
	stubCredentialStore(t, "svc-transfer", "fromstore")
	p := writeSSHConfig(t, "Host sftp.example.com\n  User fromsshconfig\n")

	r := remoteWithSSHConfig("sftp.example.com", p)
	r.User = "bob"
	r.Password = config.Secret("fromyaml")

	res, err := resolveSFTP(r)
	if err != nil {
		t.Fatal(err)
	}
	if res.User != "bob" || string(res.Password) != "fromyaml" {
		t.Errorf("resolved user=%q, want the explicit configuration to win", res.User)
	}
}

func TestResolveSFTPFallsBackToSSHConfigWhenTheStoreIsOff(t *testing.T) {
	stubCredentialStore(t, "svc-transfer", "fromstore")
	p := writeSSHConfig(t, "Host sftp.example.com\n  User fromsshconfig\n")

	r := remoteWithSSHConfig("sftp.example.com", p)
	off := false
	r.UseCredentialManager = &off

	res, err := resolveSFTP(r)
	if err != nil {
		t.Fatal(err)
	}
	if res.User != "fromsshconfig" || res.Password.IsSet() {
		t.Errorf("resolved user=%q password set=%v, want ssh_config once the store is turned off",
			res.User, res.Password.IsSet())
	}
}
