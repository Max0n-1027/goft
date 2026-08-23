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
