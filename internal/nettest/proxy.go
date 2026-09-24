// Package nettest provides a TCP proxy that can stop forwarding on demand, so
// that tests can reproduce a connection that stalls: still open, moving
// nothing, the way a dead link or a wedged server looks from the client.
package nettest

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

// Proxy forwards TCP connections to a target until it is frozen.
type Proxy struct {
	// Host and Port are where clients should connect.
	Host string
	Port int

	target      string
	freezeAfter int64 // bytes, first connection only; 0 means never

	accepted atomic.Int64
	frozen   chan struct{}
	once     sync.Once
	done     chan struct{}
}

// Start runs a proxy to target (host:port). When freezeAfter is non-zero, the
// first connection freezes once that many bytes have passed through it in
// either direction; later connections are forwarded normally, which is what a
// retry over a new connection should find.
func Start(t *testing.T, target string, freezeAfter int64) *Proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &Proxy{
		Host:        "127.0.0.1",
		Port:        ln.Addr().(*net.TCPAddr).Port,
		target:      target,
		freezeAfter: freezeAfter,
		frozen:      make(chan struct{}),
		done:        make(chan struct{}),
	}
	t.Cleanup(func() {
		close(p.done)
		ln.Close()
	})

	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			n := p.accepted.Add(1)
			go p.forward(client, n == 1)
		}
	}()
	return p
}

// Freeze stops every connection from forwarding anything further, now.
func (p *Proxy) Freeze() { p.once.Do(func() { close(p.frozen) }) }

// Connections reports how many connections the proxy has accepted.
func (p *Proxy) Connections() int { return int(p.accepted.Load()) }

func (p *Proxy) forward(client net.Conn, first bool) {
	server, err := net.Dial("tcp", p.target)
	if err != nil {
		client.Close()
		return
	}
	// A proxy frozen by hand stops every connection. One set to freeze after
	// so many bytes stops only the first, so that a retry gets through.
	freezes := p.freezeAfter == 0 || first
	limit := int64(0)
	if first {
		limit = p.freezeAfter
	}

	var moved atomic.Int64
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); p.pipe(server, client, &moved, limit, freezes) }()
	go func() { defer wg.Done(); p.pipe(client, server, &moved, limit, freezes) }()
	wg.Wait()
	client.Close()
	server.Close()
}

// pipe copies src to dst until either side closes, the test ends, or the
// connection freezes — after which it holds both ends open and moves nothing.
func (p *Proxy) pipe(dst, src net.Conn, moved *atomic.Int64, limit int64, freezes bool) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if limit > 0 && moved.Add(int64(n)) > limit {
				p.Freeze()
			}
			if freezes && p.isFrozen() {
				// Held open until the test ends, then closed at both ends, so
				// that the server lets go of the connection — and of any file
				// it had open on it — while the test can still clean up.
				<-p.done
				src.Close()
				dst.Close()
				return
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				dst.Close()
				return
			}
		}
		if err != nil {
			dst.Close()
			return
		}
	}
}

func (p *Proxy) isFrozen() bool {
	select {
	case <-p.frozen:
		return true
	default:
		return false
	}
}
