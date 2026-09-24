package fsys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goft/internal/config"
)

func writeSSHConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func remoteWithSSHConfig(host, file string) config.Remote {
	return config.Remote{Protocol: config.ProtocolSFTP, Host: host, Path: "/upload", SSHConfigFile: file}
}

func TestResolveSFTPFillsGapsFromSSHConfig(t *testing.T) {
	cfgFile := writeSSHConfig(t, `
Host invoice
  HostName sftp.example.com
  Port 2222
  User uploader
`)
	res, err := resolveSFTP(remoteWithSSHConfig("invoice", cfgFile))
	if err != nil {
		t.Fatal(err)
	}
	if res.Host != "sftp.example.com" || res.Port != 2222 || res.User != "uploader" {
		t.Errorf("resolved %s:%d as %s, want the alias expanded", res.Host, res.Port, res.User)
	}
	for _, field := range []string{"host", "port", "user"} {
		if !tracedFrom(res, field, SourceSSHConfig) {
			t.Errorf("trace = %+v, want %s attributed to ssh_config", res.Trace, field)
		}
	}
}

func TestResolveSFTPPrefersTheJobConfiguration(t *testing.T) {
	cfgFile := writeSSHConfig(t, "Host invoice\n  Port 2222\n  User uploader\n")
	r := remoteWithSSHConfig("invoice", cfgFile)
	r.Port = 22022
	r.User = "someone-else"

	res, err := resolveSFTP(r)
	if err != nil {
		t.Fatal(err)
	}
	// Port carries a real risk of confusion because 0 is a valid zero value,
	// which is why the explicit-key check exists rather than a non-zero test.
	if res.Port != 22022 || res.User != "someone-else" {
		t.Errorf("resolved port=%d user=%q, want the explicit configuration to win", res.Port, res.User)
	}
	if !tracedFrom(res, "user", SourceYAML) {
		t.Errorf("trace = %+v, want the user attributed to yaml", res.Trace)
	}
}

func TestResolveSFTPFallsBackToBuiltInDefaults(t *testing.T) {
	cfgFile := writeSSHConfig(t, "Host other\n  Port 2222\n")
	res, err := resolveSFTP(remoteWithSSHConfig("invoice", cfgFile))
	if err != nil {
		t.Fatal(err)
	}
	if res.Port != 22 {
		t.Errorf("port = %d, want 22 when no entry matches", res.Port)
	}
	if !tracedFrom(res, "port", SourceDefault) {
		t.Errorf("trace = %+v, want the port attributed to the built-in default", res.Trace)
	}
}

func TestResolveSFTPHonoursUseSSHConfigFalse(t *testing.T) {
	cfgFile := writeSSHConfig(t, "Host invoice\n  HostName sftp.example.com\n  Port 2222\n")
	r := remoteWithSSHConfig("invoice", cfgFile)
	off := false
	r.UseSSHConfig = &off

	res, err := resolveSFTP(r)
	if err != nil {
		t.Fatal(err)
	}
	if res.Host != "invoice" || res.Port != 22 {
		t.Errorf("resolved %s:%d, want the file ignored entirely", res.Host, res.Port)
	}
}

func TestResolveSFTPWarnsAboutUnsupportedDirectives(t *testing.T) {
	cfgFile := writeSSHConfig(t, "Host invoice\n  ProxyJump bastion\n")
	res, err := resolveSFTP(remoteWithSSHConfig("invoice", cfgFile))
	if err != nil {
		t.Fatal(err)
	}
	// Connecting directly usually just fails, so the reason has to be visible.
	if len(res.Warnings) == 0 {
		t.Fatal("want a warning that ProxyJump is ignored")
	}
}

func TestResolveSFTPRelaxesHostKeyCheckingOnlyLoudly(t *testing.T) {
	cfgFile := writeSSHConfig(t, "Host invoice\n  StrictHostKeyChecking no\n")
	res, err := resolveSFTP(remoteWithSSHConfig("invoice", cfgFile))
	if err != nil {
		t.Fatal(err)
	}
	if !res.SkipHostKey {
		t.Error("the alias asked for relaxed checking and that is honoured")
	}
	if len(res.Warnings) == 0 {
		t.Error("skipping host key verification must never be silent")
	}
}

func TestExpandTokens(t *testing.T) {
	requirePOSIX(t, "the joined path uses the host separator, which is a backslash on Windows")
	got := expandTokens("~/.ssh/%h_%u", "example.com", "alice", "/home/alice")
	want := "/home/alice/.ssh/example.com_alice"
	if got != want {
		t.Errorf("expandTokens() = %q, want %q: the library leaves these alone", got, want)
	}
}

func TestAnUnreadableDefaultSSHConfigIsReportedNotIgnored(t *testing.T) {
	// The parser rejects Match criteria other than host and all. For the
	// default ~/.ssh/config that error used to be swallowed, and with it every
	// setting in the file: a job relying on its Port connected to 22 instead,
	// and nothing said why.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "Host invoice\n  Port 2222\n\nMatch user root\n  User someone\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	on := true
	res, err := resolveSFTP(config.Remote{Protocol: config.ProtocolSFTP, Host: "invoice", Path: "/p", UseSSHConfig: &on})
	if err != nil {
		t.Fatal(err)
	}
	var said bool
	for _, w := range res.Warnings {
		if strings.Contains(w, "Match") {
			said = true
		}
	}
	if !said {
		t.Errorf("warnings = %v, want the unreadable ssh_config reported", res.Warnings)
	}
}

func TestTheDefaultSSHConfigIsFoundInTheHomeDirectory(t *testing.T) {
	// The same home directory as known_hosts and the default keys, found the
	// same way — which the library's own search did not do.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host invoice\n  Port 2222\n  User uploader\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	on := true
	res, err := resolveSFTP(config.Remote{Protocol: config.ProtocolSFTP, Host: "invoice", Path: "/p", UseSSHConfig: &on})
	if err != nil {
		t.Fatal(err)
	}
	if res.Port != 2222 || res.User != "uploader" {
		t.Errorf("port=%d user=%q, want both from ~/.ssh/config", res.Port, res.User)
	}
	if !tracedFrom(res, "port", SourceSSHConfig) {
		t.Errorf("trace = %+v, want the port attributed to ssh_config", res.Trace)
	}
}
