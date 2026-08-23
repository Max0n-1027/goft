package fsys

import (
	"context"
	"io"
	"sync"
)

// Op names the [FS] operations that [ErrFS] can count and fail.
type Op string

// Countable operations.
const (
	OpList     Op = "List"
	OpStat     Op = "Stat"
	OpOpen     Op = "Open"
	OpWrite    Op = "Write"
	OpMkdirAll Op = "MkdirAll"
	OpRename   Op = "Rename"
	OpRemove   Op = "Remove"
)

// ErrFS wraps an [FS] to count calls and inject failures. It exists for tests,
// which drive the whole engine over two local file systems.
type ErrFS struct {
	FS

	mu       sync.Mutex
	counts   map[Op]int
	fail     map[Op]error
	failLeft map[Op]int
	// StatResult overrides Stat for a given name, so a test can simulate a file
	// appearing on the destination in the middle of a cycle.
	statOverride map[string]func() (FileInfo, error)
}

// NewErrFS wraps inner.
func NewErrFS(inner FS) *ErrFS {
	return &ErrFS{
		FS:           inner,
		counts:       map[Op]int{},
		fail:         map[Op]error{},
		failLeft:     map[Op]int{},
		statOverride: map[string]func() (FileInfo, error){},
	}
}

// FailOp makes every subsequent call to op return err.
func (e *ErrFS) FailOp(op Op, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fail[op] = err
	e.failLeft[op] = -1
}

// FailOpTimes makes the next n calls to op return err, after which it works
// again. This is how a transient failure is simulated for the retry path.
func (e *ErrFS) FailOpTimes(op Op, err error, n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fail[op] = err
	e.failLeft[op] = n
}

// OverrideStat replaces the result of Stat for one name.
func (e *ErrFS) OverrideStat(name string, fn func() (FileInfo, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.statOverride[name] = fn
}

// Count returns how often op has been called.
func (e *ErrFS) Count(op Op) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counts[op]
}

func (e *ErrFS) enter(op Op) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.counts[op]++

	err, ok := e.fail[op]
	if !ok {
		return nil
	}
	switch left := e.failLeft[op]; {
	case left < 0: // fail every time
		return err
	case left > 0:
		e.failLeft[op] = left - 1
		return err
	default:
		delete(e.fail, op)
		return nil
	}
}

// List implements FS.
func (e *ErrFS) List(ctx context.Context, dir string) ([]Entry, error) {
	if err := e.enter(OpList); err != nil {
		return nil, err
	}
	return e.FS.List(ctx, dir)
}

// Stat implements FS.
func (e *ErrFS) Stat(ctx context.Context, name string) (FileInfo, error) {
	if err := e.enter(OpStat); err != nil {
		return FileInfo{}, err
	}
	e.mu.Lock()
	fn := e.statOverride[name]
	e.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return e.FS.Stat(ctx, name)
}

// Open implements FS.
func (e *ErrFS) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := e.enter(OpOpen); err != nil {
		return nil, err
	}
	return e.FS.Open(ctx, name)
}

// Write implements FS.
func (e *ErrFS) Write(ctx context.Context, name string, r io.Reader) (int64, error) {
	if err := e.enter(OpWrite); err != nil {
		return 0, err
	}
	return e.FS.Write(ctx, name, r)
}

// MkdirAll implements FS.
func (e *ErrFS) MkdirAll(ctx context.Context, dir string) error {
	if err := e.enter(OpMkdirAll); err != nil {
		return err
	}
	return e.FS.MkdirAll(ctx, dir)
}

// Rename implements FS.
func (e *ErrFS) Rename(ctx context.Context, from, to string) error {
	if err := e.enter(OpRename); err != nil {
		return err
	}
	return e.FS.Rename(ctx, from, to)
}

// Remove implements FS.
func (e *ErrFS) Remove(ctx context.Context, name string) error {
	if err := e.enter(OpRemove); err != nil {
		return err
	}
	return e.FS.Remove(ctx, name)
}

var _ FS = (*ErrFS)(nil)

// HostPath forwards to the wrapped file system when it is local, so that
// wrapping an [FS] for a test does not disable post-processing moves.
func (e *ErrFS) HostPath(name string) string {
	if hp, ok := e.FS.(HostPather); ok {
		return hp.HostPath(name)
	}
	return ""
}
