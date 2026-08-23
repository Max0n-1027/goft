// Package sftptest starts a real SSH server with the sftp subsystem, so that
// tests can exercise the sftp client end to end without an external service.
package sftptest

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"goft/internal/config"
)

// Start runs a real SSH server with the sftp subsystem on localhost,
// so the client implementation is exercised end to end rather than mocked.
// It returns the remote configuration needed to reach it.
func Start(t *testing.T, root string) config.Remote {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	srvCfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "tester" && string(pass) == "secret" {
				return nil, nil
			}
			return nil, errors.New("authentication failed")
		},
	}
	srvCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(conn, srvCfg)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	khPath := filepath.Join(t.TempDir(), "known_hosts")
	hostAddr := knownhosts.Normalize(fmt.Sprintf("127.0.0.1:%d", addr.Port))
	line := knownhosts.Line([]string{hostAddr}, signer.PublicKey())
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	off := false
	return config.Remote{
		Protocol:     config.ProtocolSFTP,
		Host:         "127.0.0.1",
		Port:         addr.Port,
		User:         "tester",
		Password:     config.Secret("secret"),
		KnownHosts:   khPath,
		Path:         root,
		UseSSHConfig: &off,
	}
}

func serve(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "only sessions are supported")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			return
		}
		go func() {
			for req := range requests {
				ok := req.Type == "subsystem" && len(req.Payload) >= 4 &&
					string(req.Payload[4:]) == "sftp"
				req.Reply(ok, nil)
			}
		}()
		go func() {
			server, err := sftp.NewServer(ch)
			if err != nil {
				return
			}
			defer server.Close()
			_ = server.Serve()
		}()
	}
}
