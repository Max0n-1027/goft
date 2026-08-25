//go:build windows

package fsys

import (
	"os"
	"os/exec"
	"testing"

	"goft/internal/config"
)

// The Credential Manager against the real store rather than a decoded blob.
//
// It is opt-in because it writes to the credential store of whoever runs it:
//
//	GOFT_WINCRED_TEST=1 go test -count=1 ./internal/fsys/ -run Credential -v
//
// The entry is registered under a host no job would use, and removed again
// whether the test passes or not. Nothing else in the store is read or touched.
func TestCredentialRoundTripThroughTheStore(t *testing.T) {
	if os.Getenv("GOFT_WINCRED_TEST") == "" {
		t.Skip("set GOFT_WINCRED_TEST=1 to let this write to the Windows credential store")
	}
	const host = "goft-credential-selftest.invalid"
	remote := config.Remote{Protocol: config.ProtocolFTP, Host: host}
	target := credentialTarget(remote)

	cmdkey(t, "/generic:"+target, "/user:selftest-user", "/pass:s3cret")
	t.Cleanup(func() { cmdkey(t, "/delete:"+target) })

	user, password, found := lookupCredential(remote)
	if !found {
		t.Fatalf("no credential found under %q", target)
	}
	if user != "selftest-user" {
		t.Errorf("user = %q, want %q", user, "selftest-user")
	}
	// cmdkey writes UTF-16LE, which is the branch decodeSecret has to get
	// right and the one no synthetic blob can prove.
	if password.String() == "s3cret" {
		t.Error("Secret.String() must not reveal the password")
	}
	if got := string(password); got != "s3cret" {
		t.Errorf("password = %q, want the blob decoded as UTF-16LE", got)
	}

	// use_credential_manager: false has to stop the lookup even with an entry
	// sitting there.
	off := false
	remote.UseCredentialManager = &off
	if _, _, found := lookupCredential(remote); found {
		t.Error("use_credential_manager: false still read the store")
	}
}

func cmdkey(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("cmdkey", args...).CombinedOutput(); err != nil {
		t.Fatalf("cmdkey %v: %v\n%s", args, err, out)
	}
}
