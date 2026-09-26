// Package sftptest starts a real SSH server with the sftp subsystem, so that
// tests can exercise the sftp client end to end without an external service.
package sftptest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
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
//
// Like a stock OpenSSH server it has more than one host key, ECDSA as well as
// ed25519, and like the known_hosts OpenSSH leaves behind, the one it returns
// lists only the ed25519 key. A client negotiating whichever algorithm it
// happens to prefer rather than one known_hosts can vouch for fails against
// this server exactly as it would against a real one.
//
// The user signs in with the password in the returned configuration, or with
// any of keys. Without keys the server does not offer public key
// authentication at all.
func Start(t *testing.T, root string, keys ...ssh.PublicKey) config.Remote {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaSigner, err := ssh.NewSignerFromKey(ecdsaPriv)
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
	if len(keys) > 0 {
		srvCfg.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			for _, k := range keys {
				if c.User() == "tester" && bytes.Equal(key.Marshal(), k.Marshal()) {
					return nil, nil
				}
			}
			return nil, errors.New("key not authorized")
		}
	}
	srvCfg.AddHostKey(signer)
	srvCfg.AddHostKey(ecdsaSigner)

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
