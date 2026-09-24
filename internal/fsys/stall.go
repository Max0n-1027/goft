package fsys

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// aborter is implemented by file systems that can drop their connection at
// once, from another goroutine, while a call is blocked on it. Closing the
// socket is the one thing that reliably gets such a call back: none of the
// protocol libraries honours a context once a transfer is under way.
type aborter interface {
	abort()
}

// guard watches fs for operations that stop moving data, and drops the
// connection when one has been silent for limit. The waiting call then
// returns, with an error wrapping [ErrStalled], and the engine's retry opens a
// new connection.
//
// It measures silence, not duration. A transfer is alive for as long as bytes
// keep passing through it, however long it takes; an operation that returns
// no data until it is done, such as a listing, is allowed limit to answer.
//
// fs is returned as it is when limit is zero, or when it has no connection to
// drop.
func guard(fs FS, limit time.Duration) FS {
	a, ok := fs.(aborter)
	if limit <= 0 || !ok {
		return fs
	}
	return &stallGuard{FS: fs, limit: limit, drop: a.abort}
}

type stallGuard struct {
	FS
	limit time.Duration
	drop  func()

	// last is the time of the latest sign of life, as Unix nanoseconds: an
	// operation starting or ending, or bytes passing through one. It is
	// updated for every chunk read, so it stays out of the lock.
	last atomic.Int64

	mu       sync.Mutex
	inFlight int
	timer    *time.Timer
	stalled  bool
}

func (g *stallGuard) alive() { g.last.Store(time.Now().UnixNano()) }

// begin marks an operation under way, and starts the watch if nothing else
// had. An operation on a connection already dropped fails at once.
func (g *stallGuard) begin() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stalled {
		return g.stalledErr(nil)
	}
	g.inFlight++
	g.alive()
	if g.timer == nil {
		g.timer = time.AfterFunc(g.limit, g.check)
	}
	return nil
}

// end marks an operation finished. An error it met after the connection was
// dropped is reported as the stall it is: "use of closed network connection"
// would send an operator looking for the wrong thing.
func (g *stallGuard) end(err error) error {
	g.mu.Lock()
	g.inFlight--
	stalled := g.stalled
	g.mu.Unlock()
	g.alive()
	if stalled && err != nil {
		return g.stalledErr(err)
	}
	return err
}

// check runs when the watch expires. While operations are under way and
// something moved within the limit, it watches again for the rest of it;
// otherwise the connection has been silent for the whole limit and is dropped.
func (g *stallGuard) check() {
	g.mu.Lock()
	if g.inFlight == 0 || g.stalled {
		g.timer = nil
		g.mu.Unlock()
		return
	}
	quiet := time.Since(time.Unix(0, g.last.Load()))
	if quiet < g.limit {
		g.timer.Reset(g.limit - quiet)
		g.mu.Unlock()
		return
	}
	g.stalled, g.timer = true, nil
	g.mu.Unlock()
	g.drop()
}

func (g *stallGuard) stalledErr(cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: nothing moved for %v, so the connection was dropped", ErrStalled, g.limit)
	}
	// The cause is kept as text only. What a dropped connection reports is not
	// a statement about the file, and must not be mistaken for one — a missing
	// file, say, which would not be retried.
	return fmt.Errorf("%w: nothing moved for %v, so the connection was dropped (%v)", ErrStalled, g.limit, cause)
}

// List implements FS.
func (g *stallGuard) List(ctx context.Context, dir string) ([]Entry, error) {
	if err := g.begin(); err != nil {
		return nil, err
	}
	entries, err := g.FS.List(ctx, dir)
	return entries, g.end(err)
}

// Stat implements FS.
func (g *stallGuard) Stat(ctx context.Context, name string) (FileInfo, error) {
	if err := g.begin(); err != nil {
		return FileInfo{}, err
	}
	fi, err := g.FS.Stat(ctx, name)
	return fi, g.end(err)
}

// Open implements FS. The operation lasts until the reader is closed, and
// every chunk read from it counts as the connection being alive.
func (g *stallGuard) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := g.begin(); err != nil {
		return nil, err
	}
	rc, err := g.FS.Open(ctx, name)
	if err != nil {
		return nil, g.end(err)
	}
	return &watchedReader{r: rc, g: g}, nil
}

// Write implements FS. Every chunk taken from r counts as the connection
// being alive: the protocol only takes more once the server has room for it.
func (g *stallGuard) Write(ctx context.Context, name string, r io.Reader) (int64, error) {
	if err := g.begin(); err != nil {
		return 0, err
	}
	n, err := g.FS.Write(ctx, name, &watchedReader{r: r, g: g, borrowed: true})
	return n, g.end(err)
}

// MkdirAll implements FS.
func (g *stallGuard) MkdirAll(ctx context.Context, dir string) error {
	if err := g.begin(); err != nil {
		return err
	}
	return g.end(g.FS.MkdirAll(ctx, dir))
}

// Rename implements FS.
func (g *stallGuard) Rename(ctx context.Context, from, to string) error {
	if err := g.begin(); err != nil {
		return err
	}
	return g.end(g.FS.Rename(ctx, from, to))
}

// Remove implements FS.
func (g *stallGuard) Remove(ctx context.Context, name string) error {
	if err := g.begin(); err != nil {
		return err
	}
	return g.end(g.FS.Remove(ctx, name))
}

// Close implements FS.
func (g *stallGuard) Close() error {
	g.mu.Lock()
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	g.mu.Unlock()
	return g.FS.Close()
}

// watchedReader reports every chunk that passes through it as a sign of life.
// One returned by Open also ends its operation when closed; one wrapping the
// caller's reader for Write is only borrowed, and the Write ends it.
type watchedReader struct {
	r        io.Reader
	g        *stallGuard
	borrowed bool
	closed   sync.Once
}

func (w *watchedReader) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	if n > 0 {
		w.g.alive()
	}
	if err != nil && err != io.EOF && !w.borrowed {
		w.g.mu.Lock()
		stalled := w.g.stalled
		w.g.mu.Unlock()
		if stalled {
			err = w.g.stalledErr(err)
		}
	}
	return n, err
}

func (w *watchedReader) Close() error {
	var err error
	w.closed.Do(func() {
		if c, ok := w.r.(io.Closer); ok {
			err = c.Close()
		}
		err = w.g.end(err)
	})
	return err
}
