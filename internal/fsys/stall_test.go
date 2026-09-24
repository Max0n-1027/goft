package fsys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/ftptest"
	"goft/internal/nettest"
	"goft/internal/sftptest"
)

const stallLimit = 500 * time.Millisecond

// throughProxy points r at a proxy in front of its server.
func throughProxy(t *testing.T, r config.Remote, freezeAfter int64) (config.Remote, *nettest.Proxy) {
	t.Helper()
	port := r.Port
	if port == 0 {
		port = map[config.Protocol]int{config.ProtocolSFTP: 22, config.ProtocolFTP: 21, config.ProtocolSMB: 445}[r.Protocol]
	}
	p := nettest.Start(t, net.JoinHostPort(r.Host, strconv.Itoa(port)), freezeAfter)
	r.Host, r.Port = p.Host, p.Port
	r.IOTimeout = stallLimit
	// The proxy's address is not the one known_hosts lists.
	r.KnownHosts, r.InsecureSkipHostKeyCheck = "", true
	return r, p
}

// within runs op and fails the test if it has not returned after a generous
// multiple of stallLimit — which is what a stalled connection did before, for
// ever.
func within(t *testing.T, op func() error) error {
	t.Helper()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- op() }()
	select {
	case err := <-done:
		if took := time.Since(start); took > 10*stallLimit {
			t.Errorf("gave up after %v, want about %v", took, stallLimit)
		}
		return err
	case <-time.After(20 * stallLimit):
		t.Fatalf("still waiting on a stalled connection after %v", 20*stallLimit)
		return nil
	}
}

// checkStalledTransfer sends a file through a proxy that goes silent part way
// through, and wants the transfer abandoned after io_timeout rather than
// waiting on it for good.
func checkStalledTransfer(t *testing.T, r config.Remote) {
	t.Helper()
	r, _ = throughProxy(t, r, 1<<20)
	fs, err := NewRemote(context.Background(), r)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer fs.Close()

	name := fmt.Sprintf("stalled-%d.dat", time.Now().UnixNano())
	err = within(t, func() error {
		_, err := fs.Write(context.Background(), name, bytes.NewReader(make([]byte, 8<<20)))
		return err
	})
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("Write = %v, want ErrStalled", err)
	}
	// The connection is gone; anything else on it says so at once rather than
	// waiting another io_timeout to find out.
	if err := within(t, func() error { _, err := fs.List(context.Background(), ""); return err }); !errors.Is(err, ErrStalled) {
		t.Errorf("List after the stall = %v, want ErrStalled straight away", err)
	}
}

func TestSFTPStalledTransferIsAbandoned(t *testing.T) {
	checkStalledTransfer(t, sftptest.Start(t, t.TempDir()))
}

func TestFTPStalledControlConnectionIsAbandoned(t *testing.T) {
	// FTP's data connections go straight to the server, not through the proxy,
	// so this freezes the control connection instead: a listing waiting on it
	// is what stalls.
	r, p := throughProxy(t, ftptest.Start(t, t.TempDir()), 0)
	fs, err := NewRemote(context.Background(), r)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer fs.Close()

	p.Freeze()
	err = within(t, func() error { _, err := fs.List(context.Background(), ""); return err })
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("List = %v, want ErrStalled", err)
	}
}

func TestLiveStalledTransferIsAbandoned(t *testing.T) {
	r := liveRemote(t)
	if r.Protocol == config.ProtocolFTP {
		t.Skip("an FTP data connection does not go through the proxy")
	}
	// Registered first so that it runs last, after the proxy has let go of the
	// frozen connection: until then the server still holds the partial file
	// open, and an SMB server will not delete it. It lets go in its own time,
	// so this tries a few times.
	t.Cleanup(func() {
		fs, err := NewRemote(context.Background(), r)
		if err != nil {
			return
		}
		defer fs.Close()
		for range 10 {
			entries, _ := fs.List(context.Background(), "")
			left := 0
			for _, e := range entries {
				if strings.HasPrefix(e.Name, "stalled-") {
					if fs.Remove(context.Background(), e.Name) != nil {
						left++
					}
				}
			}
			if left == 0 {
				return
			}
			time.Sleep(time.Second)
		}
		t.Error("could not remove the partial file the stalled transfer left behind")
	})
	checkStalledTransfer(t, r)
}

// fakeConn is an FS whose calls can be made to hang until the connection is
// dropped, standing in for a server that has gone quiet.
type fakeConn struct {
	FS
	dropped chan struct{}
	once    sync.Once
}

func newFakeConn(t *testing.T) *fakeConn {
	return &fakeConn{FS: NewLocal(t.TempDir()), dropped: make(chan struct{})}
}

func (f *fakeConn) abort() { f.once.Do(func() { close(f.dropped) }) }

// Stat hangs until the connection is dropped.
func (f *fakeConn) Stat(context.Context, string) (FileInfo, error) {
	<-f.dropped
	return FileInfo{}, errors.New("use of closed network connection")
}

// trickle yields one byte at a time with a pause before each.
type trickle struct {
	left  int
	pause time.Duration
}

func (r *trickle) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	time.Sleep(r.pause)
	p[0] = 'x'
	r.left--
	return 1, nil
}

func TestASlowTransferThatKeepsMovingIsNotCutOff(t *testing.T) {
	// io_timeout measures silence, not duration. This one takes three times as
	// long as the limit, but something moves well within every stretch of it.
	f := newFakeConn(t)
	g := guard(f, stallLimit)

	n, err := g.Write(context.Background(), "slow.dat", &trickle{left: 12, pause: stallLimit / 4})
	if err != nil || n != 12 {
		t.Fatalf("Write = %d, %v; want all of it, uncut", n, err)
	}
	select {
	case <-f.dropped:
		t.Error("the connection was dropped under a transfer that never went quiet")
	default:
	}
}

func TestAnOperationThatSaysNothingIsCutOff(t *testing.T) {
	f := newFakeConn(t)
	g := guard(f, stallLimit)

	err := within(t, func() error { _, err := g.Stat(context.Background(), "x"); return err })
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("Stat = %v, want ErrStalled", err)
	}
	// What the dropped connection said is kept, but only as text: it is not a
	// statement about the file.
	if !strings.Contains(err.Error(), "use of closed network connection") {
		t.Errorf("error = %v, want the underlying reason kept", err)
	}
}

func TestIdleTimeBetweenOperationsIsNotSilence(t *testing.T) {
	// Nothing waiting on the connection means nothing to time out: a worker
	// between files, or a connection held while others finish, is not stalled.
	f := newFakeConn(t)
	g := guard(f, stallLimit)
	if _, err := g.List(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * stallLimit)
	if _, err := g.List(context.Background(), ""); err != nil {
		t.Errorf("List after an idle spell = %v, want it to work", err)
	}
}

func TestNoLimitMeansNoGuard(t *testing.T) {
	f := newFakeConn(t)
	if g := guard(f, 0); g != FS(f) {
		t.Error("io_timeout: 0 should leave the connection unwatched")
	}
	if l := NewLocal(t.TempDir()); guard(l, stallLimit) != FS(l) {
		t.Error("a local directory has no connection to drop and should be left alone")
	}
}

func TestSFTPConformanceUnderTheGuard(t *testing.T) {
	// Everything a protocol has to get right, with the guard in the way.
	remote := sftptest.Start(t, t.TempDir())
	remote.IOTimeout = 5 * time.Second
	runFSConformance(t, func(t *testing.T) FS {
		f, err := NewRemote(context.Background(), remote)
		if err != nil {
			t.Fatal(err)
		}
		return f
	})
}

func TestFTPConformanceUnderTheGuard(t *testing.T) {
	remote := ftptest.Start(t, t.TempDir())
	remote.IOTimeout = 5 * time.Second
	runFSConformance(t, func(t *testing.T) FS {
		f, err := NewRemote(context.Background(), remote)
		if err != nil {
			t.Fatal(err)
		}
		return f
	})
}
