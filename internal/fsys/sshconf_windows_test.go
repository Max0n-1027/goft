//go:build windows

package fsys

import (
	"path/filepath"
	"testing"
)

// The POSIX form of this is TestExpandTokens, which requirePOSIX skips here.
//
// ssh_config is written with forward slashes wherever it comes from, but every
// path it yields — IdentityFile, UserKnownHostsFile — is opened locally, so
// expanding "~" against a Windows home has to produce a Windows path.
func TestExpandTokensProducesAWindowsPath(t *testing.T) {
	home := `C:\Users\alice`
	got := expandTokens("~/.ssh/%h_%u", "example.com", "alice", home)
	want := filepath.Join(home, ".ssh", "example.com_alice")
	if got != want {
		t.Errorf("expandTokens() = %q, want %q", got, want)
	}
}

// A path already given in Windows form is left as it is.
func TestExpandTokensLeavesAnAbsoluteWindowsPathAlone(t *testing.T) {
	in := `C:\keys\id_ed25519`
	if got := expandTokens(in, "example.com", "alice", `C:\Users\alice`); got != in {
		t.Errorf("expandTokens() = %q, want %q", got, in)
	}
}
