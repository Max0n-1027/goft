package fsys

import (
	"fmt"
	"unicode/utf16"

	"github.com/danieljoos/wincred"

	"goft/internal/config"
)

// SourceCredentialManager marks a value that came from the Windows Credential
// Manager.
const SourceCredentialManager = "credential_manager"

// credentialTarget is the name a credential is registered under.
//
// Prefixing with the tool name is Microsoft's own guidance for generic
// credentials, and it keeps goft from picking up an entry some other program
// stored for the same host.
func credentialTarget(r config.Remote) string {
	if r.CredentialTarget != "" {
		return r.CredentialTarget
	}
	return fmt.Sprintf("goft:%s://%s", r.Protocol, r.Host)
}

// lookupCredential reads the user and password stored for this job in the
// Windows Credential Manager.
//
// Only generic credentials can be read. The ones Windows keeps for network
// shares are domain credentials, whose blob the documentation reserves for the
// authentication packages, so a share that Explorer or `net use` remembered
// cannot be reused here however much one would like it to. What this does
// support is an entry registered for goft itself:
//
//	cmdkey /generic:goft:smb://fileserver /user:svc-transfer /pass:secret
//
// Elsewhere than Windows the call is simply unsupported, and the lookup
// reports nothing found.
func lookupCredential(r config.Remote) (user string, password config.Secret, found bool) {
	if !r.CredentialManagerEnabled() {
		return "", "", false
	}
	cred, err := wincred.GetGenericCredential(credentialTarget(r))
	if err != nil || cred == nil {
		return "", "", false
	}
	return cred.UserName, config.Secret(decodeSecret(cred.CredentialBlob)), true
}

// decodeSecret turns a credential blob into the password it holds.
//
// The blob of a generic credential is whatever the tool that wrote it chose to
// put there. cmdkey and the Credential Manager window, being Windows programs
// handling wide strings, write UTF-16LE; tools ported from elsewhere tend to
// write UTF-8.
//
// A zero byte is the tell: UTF-8 text cannot contain one, because no byte of a
// multi-byte sequence is zero. A blob of even length carrying one is therefore
// UTF-16LE. The remaining ambiguity is a UTF-16LE password made entirely of
// characters outside Latin-1, which has no zero bytes and is read as UTF-8;
// that is the case to reach for credential_target and a tool of your own if it
// ever bites.
func decodeSecret(blob []byte) string {
	if len(blob) == 0 {
		return ""
	}
	if len(blob)%2 != 0 || !containsZero(blob) {
		return string(blob)
	}

	units := make([]uint16, 0, len(blob)/2)
	for i := 0; i < len(blob); i += 2 {
		units = append(units, uint16(blob[i])|uint16(blob[i+1])<<8)
	}
	return string(utf16.Decode(units))
}

func containsZero(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

// applyCredential fills in whatever the job left out from the credential store,
// recording where each value came from.
//
// The configuration file always wins: the store is a place to keep a password
// out of the file, not a way to override one that is in it.
func applyCredential(res *Resolved, r config.Remote) {
	user, password, found := lookupCredential(r)
	if !found {
		return
	}
	if res.User == "" && user != "" {
		res.User = user
		res.record("user", user, SourceCredentialManager)
	}
	if !res.Password.IsSet() && password.IsSet() {
		res.Password = password
		res.record("password", password.String(), SourceCredentialManager)
	}
}
