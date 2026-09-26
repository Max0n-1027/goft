package cmd

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"goft/internal/sftptest"
)

func TestASendAuthenticatedByTheAgentAlone(t *testing.T) {
	// No password and no key file: the only way in is the key the agent holds,
	// found where the job says the agent is.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	localDir, remoteDir := t.TempDir(), t.TempDir()
	server := sftptest.Start(t, remoteDir, sshPub)
	sock := sftptest.StartAgent(t, priv)

	cfg := filepath.Join(t.TempDir(), "job.yaml")
	body := fmt.Sprintf(`
local:
  path: %s
remote:
  protocol: sftp
  host: %s
  port: %d
  user: %s
  known_hosts: %s
  path: %s
  use_ssh_config: false
  ssh_agent: %s
stable_duration: 0s
`, localDir, server.Host, server.Port, server.User, server.KnownHosts, remoteDir, sock)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "a.txt"), []byte("through the agent"), 0o600); err != nil {
		t.Fatal(err)
	}

	resetFlags()
	rootCmd.SetArgs([]string{"send", "-c", cfg, "--no-console"})
	rootCmd.SetOut(os.Stdout)
	if code := Execute(); code != 0 {
		t.Fatalf("send exit code = %d, want 0", code)
	}
	if got, err := os.ReadFile(filepath.Join(remoteDir, "a.txt")); err != nil || string(got) != "through the agent" {
		t.Fatalf("remote a.txt = %q, %v", got, err)
	}

	var out strings.Builder
	resetFlags()
	rootCmd.SetArgs([]string{"test", "-c", cfg})
	rootCmd.SetOut(&out)
	code := Execute()
	rootCmd.SetOut(os.Stdout)
	if code != 0 {
		t.Fatalf("test exit code = %d, want 0\n%s", code, out.String())
	}
	if want := "ssh_agent = " + sock + " (holds 1 key)"; !strings.Contains(out.String(), want) {
		t.Errorf("goft test output does not say %q:\n%s", want, out.String())
	}
}
