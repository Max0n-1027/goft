//go:build windows

package fsys

import (
	"os"
	"path/filepath"
	"testing"

	"goft/internal/config"
)

// The POSIX form of this is TestResolveFTPWarnsAboutLoosePermissions, which
// requirePOSIX skips here.
//
// Windows has no mode bits to read — os.Stat reports 0666 whatever the ACL
// says — so warning about them would be telling the operator to chmod
// something that has no mode. Saying nothing is the honest answer.
func TestResolveFTPDoesNotWarnAboutPermissionsHere(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".netrc")
	writeFile(t, p, "machine ftp.example.com login alice password s3cret\n")

	res, err := resolveFTP(ftpRemoteWithoutTheStore("ftp.example.com", p))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none: there are no mode bits here to complain about", res.Warnings)
	}
	if res.User != "alice" {
		t.Errorf("User = %q, want the credentials to still be read", res.User)
	}
}

// README tells Windows operators to put the file at %USERPROFILE%\.netrc, and
// os.UserHomeDir is what turns that into a path. This is the whole lookup, not
// just the candidate list that netrc_test.go covers for every platform.
func TestNetrcIsFoundUnderTheUserProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("NETRC", "")
	writeFile(t, filepath.Join(home, ".netrc"), "machine ftp.example.com login alice password s3cret\n")

	res, err := resolveFTP(ftpRemoteWithoutTheStore("ftp.example.com", ""))
	if err != nil {
		t.Fatal(err)
	}
	if res.User != "alice" || res.Password.String() == "" {
		t.Errorf("User = %q: %%USERPROFILE%%\\.netrc was not read", res.User)
	}
}

// The underscore spelling that ports of Unix tools leave behind is still
// accepted here, and nowhere else.
func TestUnderscoreNetrcIsFoundUnderTheUserProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("NETRC", "")
	writeFile(t, filepath.Join(home, "_netrc"), "machine ftp.example.com login bob password s3cret\n")

	res, err := resolveFTP(ftpRemoteWithoutTheStore("ftp.example.com", ""))
	if err != nil {
		t.Fatal(err)
	}
	if res.User != "bob" {
		t.Errorf("User = %q, want %%USERPROFILE%%\\_netrc to have been read", res.User)
	}
}

// ftpRemoteWithoutTheStore describes an ftp job that has to fall back to netrc,
// with the Credential Manager turned off so that an entry the machine running
// the tests happens to hold cannot answer first.
func ftpRemoteWithoutTheStore(host, netrcFile string) config.Remote {
	off := false
	return config.Remote{
		Protocol:             config.ProtocolFTP,
		Host:                 host,
		NetrcFile:            netrcFile,
		UseCredentialManager: &off,
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
