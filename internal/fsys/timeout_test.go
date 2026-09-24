package fsys

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/ftptest"
	"goft/internal/sftptest"
)

// silentServer accepts connections and never says a word: the shape of a
// server that is up but wedged, or of something else entirely listening on the
// port. It returns the port.
func silentServer(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(io.Discard, c)
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestConnectingToASilentServerGivesUp(t *testing.T) {
	// The TCP connection succeeds at once, so a dial timeout alone never fires.
	// Everything after — the SSH handshake, the FTP greeting, SMB negotiation —
	// waited for as long as the server cared to say nothing, which held a
	// watching job there for good.
	const limit = 300 * time.Millisecond
	off := false

	for _, r := range []config.Remote{
		{Protocol: config.ProtocolSFTP, User: "u", Password: config.Secret("p"),
			InsecureSkipHostKeyCheck: true, UseSSHConfig: &off},
		{Protocol: config.ProtocolFTP, User: "u", Password: config.Secret("p"), UseNetrc: &off},
		{Protocol: config.ProtocolSMB, User: "u", Password: config.Secret("p"), Share: "s"},
	} {
		t.Run(string(r.Protocol), func(t *testing.T) {
			off := false
			r.UseCredentialManager = &off
			r.Host, r.Port, r.Path = "127.0.0.1", silentServer(t), "/"
			r.ConnectTimeout = limit

			done := make(chan error, 1)
			start := time.Now()
			go func() {
				fs, err := NewRemote(context.Background(), r)
				if err == nil {
					fs.Close()
				}
				done <- err
			}()

			select {
			case err := <-done:
				if err == nil {
					t.Fatal("connected to a server that never answered")
				}
				// The operator should read why, not just "i/o timeout".
				if !strings.Contains(err.Error(), "connect_timeout") {
					t.Errorf("error = %v, want it to name connect_timeout", err)
				}
				if took := time.Since(start); took > 10*limit {
					t.Errorf("gave up after %v, want about %v", took, limit)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("still waiting for a silent server after 5s")
			}
		})
	}
}

// checkOutlivesConnectTimeout opens a connection with a short connect_timeout
// and uses it well after that has passed. The deadline covers establishing the
// connection only; left in place, it would cut off the first transfer that ran
// longer than it.
func checkOutlivesConnectTimeout(t *testing.T, r config.Remote) {
	t.Helper()
	const limit = 500 * time.Millisecond
	r.ConnectTimeout = limit

	fs, err := NewRemote(context.Background(), r)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer fs.Close()

	time.Sleep(2 * limit)
	if _, err := fs.List(context.Background(), ""); err != nil {
		t.Fatalf("the connection did not outlive connect_timeout: %v", err)
	}
	name := "outlives-connect-timeout.txt"
	defer fs.Remove(context.Background(), name)
	if _, err := fs.Write(context.Background(), name, strings.NewReader("still here")); err != nil {
		t.Fatalf("a transfer after connect_timeout had passed: %v", err)
	}
}

func TestSFTPConnectionOutlivesItsConnectTimeout(t *testing.T) {
	checkOutlivesConnectTimeout(t, sftptest.Start(t, t.TempDir()))
}

func TestFTPConnectionOutlivesItsConnectTimeout(t *testing.T) {
	checkOutlivesConnectTimeout(t, ftptest.Start(t, t.TempDir()))
}

func TestLiveConnectionOutlivesItsConnectTimeout(t *testing.T) {
	checkOutlivesConnectTimeout(t, liveRemote(t))
}

func TestLiveATransferUnderWayOutlivesAStopRequest(t *testing.T) {
	// A stop request cancels the context the connection was opened with. The
	// file under way is meant to finish regardless — only SMB kept that context
	// for every call, and cut the transfer short with it.
	r := liveRemote(t)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	fs, err := NewRemote(ctx, r)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer fs.Close()

	name := fmt.Sprintf("outlives-stop-%d.dat", time.Now().UnixNano())
	defer fs.Remove(context.Background(), name)
	go func() {
		time.Sleep(50 * time.Millisecond)
		stop()
	}()
	payload := make([]byte, 32<<20)
	n, err := fs.Write(ctx, name, bytes.NewReader(payload))
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("Write = %d, %v; want the whole file despite the stop", n, err)
	}
	if ctx.Err() == nil {
		t.Skip("the transfer finished before the stop arrived; nothing was tested")
	}
}
