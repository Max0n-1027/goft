package fsys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goft/internal/config"
)

func TestParseNetrcReadsMachineEntries(t *testing.T) {
	entries, err := parseNetrc(strings.NewReader(`
machine ftp.example.com login alice password s3cret
machine other.example.com
	login bob
	password hunter2
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if e, _, _ := lookupNetrc(entries, "ftp.example.com"); e.login != "alice" || e.password != "s3cret" {
		t.Errorf("entry = %+v, want alice/s3cret", e)
	}
	if e, _, _ := lookupNetrc(entries, "other.example.com"); e.login != "bob" {
		t.Errorf("entry = %+v, want a directive spread over several lines to parse", e)
	}
}

func TestParseNetrcSkipsMacdefBodies(t *testing.T) {
	// A macdef body runs to the next blank line. Parsing it as directives would
	// pick up the words inside as if they were logins and passwords.
	entries, err := parseNetrc(strings.NewReader(`
machine ftp.example.com login alice password s3cret

macdef init
cd /upload
machine bogus login mallory password gotcha

machine second.example.com login bob password pw2
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := lookupNetrc(entries, "bogus"); ok {
		t.Error("the macro body must not produce a machine entry")
	}
	if e, _, ok := lookupNetrc(entries, "second.example.com"); !ok || e.login != "bob" {
		t.Errorf("entry after the macro = %+v (found=%v), want bob", e, ok)
	}
}

func TestParseNetrcFallsBackToDefault(t *testing.T) {
	entries, err := parseNetrc(strings.NewReader(
		"machine known login alice password a\ndefault login guest password g\n"))
	if err != nil {
		t.Fatal(err)
	}
	e, kind, ok := lookupNetrc(entries, "unlisted")
	if !ok || e.login != "guest" || kind != "default" {
		t.Errorf("lookup = %+v %q %v, want the default entry", e, kind, ok)
	}
	if e, kind, _ := lookupNetrc(entries, "known"); e.login != "alice" || kind != "machine" {
		t.Error("a machine entry must win over the default entry")
	}
}

func TestParseNetrcConsumesAccountArgument(t *testing.T) {
	entries, err := parseNetrc(strings.NewReader(
		"machine h login alice account password password realpw\n"))
	if err != nil {
		t.Fatal(err)
	}
	// "account password" must be consumed as a pair; otherwise the word
	// "password" would be read as a directive and swallow the wrong token.
	if e, _, _ := lookupNetrc(entries, "h"); e.password != "realpw" {
		t.Errorf("password = %q, want realpw", e.password)
	}
}

func TestNetrcHostMatchIgnoresCase(t *testing.T) {
	entries, _ := parseNetrc(strings.NewReader("machine FTP.Example.COM login alice password a\n"))
	if _, _, ok := lookupNetrc(entries, "ftp.example.com"); !ok {
		t.Error("host names should match without regard to case")
	}
}

func writeNetrc(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "netrc")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveFTPFillsCredentialsFromNetrc(t *testing.T) {
	p := writeNetrc(t, "machine ftp.example.com login alice password s3cret\n", 0o600)
	res, err := resolveFTP(config.Remote{
		Protocol:  config.ProtocolFTP,
		Host:      "ftp.example.com",
		NetrcFile: p,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.User != "alice" || string(res.Password) != "s3cret" {
		t.Errorf("resolved user=%q password set=%v, want them taken from netrc", res.User, res.Password.IsSet())
	}
	if res.Port != 21 {
		t.Errorf("port = %d, want the ftp default 21", res.Port)
	}
	if !tracedFrom(res, "user", SourceNetrc) {
		t.Errorf("trace = %+v, want the user attributed to netrc", res.Trace)
	}
}

func TestResolveFTPPrefersTheJobConfiguration(t *testing.T) {
	p := writeNetrc(t, "machine ftp.example.com login alice password fromnetrc\n", 0o600)
	res, err := resolveFTP(config.Remote{
		Protocol:  config.ProtocolFTP,
		Host:      "ftp.example.com",
		User:      "bob",
		Password:  config.Secret("fromyaml"),
		NetrcFile: p,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.User != "bob" || string(res.Password) != "fromyaml" {
		t.Errorf("resolved user=%q, want the explicit configuration to win", res.User)
	}
}

func TestResolveFTPHonoursUseNetrcFalse(t *testing.T) {
	p := writeNetrc(t, "machine ftp.example.com login alice password s3cret\n", 0o600)
	off := false
	res, err := resolveFTP(config.Remote{
		Protocol:  config.ProtocolFTP,
		Host:      "ftp.example.com",
		NetrcFile: p,
		UseNetrc:  &off,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.User != "" || res.Password.IsSet() {
		t.Error("use_netrc: false must stop the file being read at all")
	}
}

func TestResolveFTPWarnsAboutLoosePermissions(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root makes the permission check moot")
	}
	p := writeNetrc(t, "machine ftp.example.com login alice password s3cret\n", 0o644)
	res, err := resolveFTP(config.Remote{
		Protocol:  config.ProtocolFTP,
		Host:      "ftp.example.com",
		NetrcFile: p,
	})
	if err != nil {
		t.Fatal(err)
	}
	// curl warns and carries on rather than refusing, so a working setup keeps
	// working while the operator is told to tighten the file.
	if len(res.Warnings) == 0 {
		t.Error("want a warning about the file being readable by other users")
	}
	if res.User != "alice" {
		t.Error("the credentials should still be used")
	}
}

func TestResolveNeverPutsSecretsInTheTrace(t *testing.T) {
	p := writeNetrc(t, "machine ftp.example.com login alice password s3cret\n", 0o600)
	res, err := resolveFTP(config.Remote{Protocol: config.ProtocolFTP, Host: "ftp.example.com", NetrcFile: p})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range res.Trace {
		if strings.Contains(tr.Value, "s3cret") {
			t.Fatalf("trace entry %+v leaks the password", tr)
		}
	}
}

func tracedFrom(res *Resolved, field, source string) bool {
	for _, t := range res.Trace {
		if t.Field == field && strings.HasPrefix(t.Source, source) {
			return true
		}
	}
	return false
}
