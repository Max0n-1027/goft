// Package ftptest starts an FTP server backed by a real directory, so that
// tests can exercise the ftp client end to end without an external service.
//
// The client implementation carries more protocol-specific reasoning than the
// others — a stat command that does not exist, one directory created at a
// time, replies that have to be classified as transient or permanent — which
// is exactly the code that benefits from being run rather than read.
//
// It is not a substitute for a real server. Two behaviours goft defends
// against are this server's opposite: it errors on LIST of a missing path
// where vsftpd returns an empty listing, and it accepts DELE on a directory
// where vsftpd requires RMD. Those defences are only exercised by the live
// suite in fsys, pointed at a real server through the GOFT_LIVE_* variables.
package ftptest

import (
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"

	ftpserver "github.com/fclairamb/ftpserverlib"
	"github.com/spf13/afero"

	"goft/internal/config"
)

const (
	testUser     = "tester"
	testPassword = "secret"
)

// Start runs a server rooted at dir on localhost and returns the remote
// configuration needed to reach it. The server stops with the test.
func Start(t *testing.T, root string) config.Remote {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := ftpserver.NewFtpServer(&driver{root: root, listener: ln})
	// The server's own chatter would drown the test output.
	server.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := server.Listen(); err != nil {
		t.Fatalf("start ftp server: %v", err)
	}
	go func() {
		// Serve returns when the listener closes, which is the normal way this
		// server ends.
		_ = server.Serve()
	}()
	t.Cleanup(func() { _ = server.Stop() })

	off := false
	return config.Remote{
		Protocol: config.ProtocolFTP,
		Host:     "127.0.0.1",
		Port:     ln.Addr().(*net.TCPAddr).Port,
		User:     testUser,
		Password: config.Secret(testPassword),
		Path:     "/",
		UseNetrc: &off,
	}
}

// driver serves one directory to one hard-coded account.
type driver struct {
	root     string
	listener net.Listener
}

func (d *driver) GetSettings() (*ftpserver.Settings, error) {
	return &ftpserver.Settings{
		Listener:   d.listener,
		PublicHost: "127.0.0.1",
		// Passive mode is what the client uses, and the tests run on one host,
		// so any free port will do.
		PassiveTransferPortRange: nil,
	}, nil
}

func (d *driver) ClientConnected(ftpserver.ClientContext) (string, error) {
	return "goft test server", nil
}

func (d *driver) ClientDisconnected(ftpserver.ClientContext) {}

func (d *driver) AuthUser(_ ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	if user != testUser || pass != testPassword {
		return nil, fmt.Errorf("authentication failed")
	}
	// Rooted at the directory under test, so a client cannot wander out of it.
	return afero.NewBasePathFs(afero.NewOsFs(), d.root), nil
}

func (d *driver) GetTLSConfig() (*tls.Config, error) {
	return nil, fmt.Errorf("TLS is not offered by the test server")
}
