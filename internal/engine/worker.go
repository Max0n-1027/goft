package engine

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"time"

	"github.com/cespare/xxhash/v2"

	"goft/internal/config"
	"goft/internal/fsys"
	"goft/internal/logging"
	"goft/internal/postact"
	"goft/internal/verify"
)

// attempt transfers one file, trying again when the failure looks like
// something that could go differently next time.
//
// The connection is rebuilt between attempts. A dropped connection is the most
// likely reason to be here, and reusing the dead one would fail identically;
// the cost of reconnecting is small next to re-sending a file.
func (e *Engine) attempt(ctx context.Context, c *conn, t target, idx *destIndex) Result {
	for n := 1; ; n++ {
		res := e.tryOnce(ctx, c, t, idx)
		res.Attempts = n

		// A post-action failure is not a transfer failure: the file reached the
		// destination, and post-processing has already retried on its own.
		if res.Outcome != Failed || res.postActionFailed ||
			n >= e.cfg.Retry.MaxAttempts || !Retryable(res.Err) {
			return res
		}
		c.invalidate()
		if !e.pause(ctx, n, t.file.Path, res.Err) {
			return res
		}
	}
}

// tryOnce makes sure there is a connection and performs one transfer.
func (e *Engine) tryOnce(ctx context.Context, c *conn, t target, idx *destIndex) Result {
	if err := c.ensure(ctx); err != nil {
		return Result{
			Index: t.index, Total: t.total, Path: t.file.Path, Bytes: t.file.Size,
			Outcome: Failed, Err: err,
		}
	}
	return e.transfer(ctx, c, t, idx)
}

// pause waits before the next attempt, and reports whether waiting completed
// rather than the run being cancelled.
func (e *Engine) pause(ctx context.Context, attempt int, path string, cause error) bool {
	wait := e.cfg.Retry.Wait(attempt)
	e.log.Warn("retrying transfer",
		logging.KeyEvent, logging.EventTransfer,
		logging.KeySrc, e.source(path),
		logging.KeyAttempt, attempt+1,
		"of", e.cfg.Retry.MaxAttempts,
		"retry_in_ms", wait.Milliseconds(),
		logging.KeyError, cause.Error())

	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

// transfer handles one file end to end and never returns an error: a failure is
// reported as a [Result] so that the rest of the cycle carries on.
func (e *Engine) transfer(ctx context.Context, c *conn, t target, idx *destIndex) Result {
	start := e.now()
	res := Result{Index: t.index, Total: t.total, Path: t.file.Path, Bytes: t.file.Size}
	finish := func(r Result) Result {
		r.Elapsed = e.now().Sub(start)
		return r
	}
	fail := func(err error) Result {
		res.Outcome, res.Err = Failed, err
		return finish(res)
	}

	if t.overSizeCap {
		res.Outcome, res.Reason, res.Recurring = Skipped, ReasonSizeLimit, t.recurring
		return finish(res)
	}

	name := t.file.Path
	dir := fsys.Dir(name)
	base := path.Base(name)

	if err := e.step(ctx, name, "mkdir", func() error { return idx.ensureDir(ctx, c.dst, dir) }); err != nil {
		return fail(err)
	}

	switch decision, err := e.decideExisting(ctx, c, t, idx, dir, base); {
	case err != nil:
		return fail(err)
	case decision == skipExisting:
		res.Outcome, res.Reason = Skipped, ReasonAlreadyExists
		return finish(res)
	case decision == skipIdentical:
		// Already there and byte for byte the same, so the transfer is complete
		// as far as the source is concerned.
		if err := e.postAction(ctx, c, name); err != nil {
			res.postActionFailed = true
			return fail(err)
		}
		res.Outcome, res.Reason = Skipped, ReasonIdentical
		return finish(res)
	}

	tmp := fsys.TempName(name)
	sent, srcHash, err := e.upload(ctx, c, name, tmp)
	if err != nil {
		e.discard(ctx, c.dst, tmp)
		return fail(err)
	}
	res.Bytes = sent
	res.SrcHash, res.Hashed = srcHash, e.cfg.Verify == config.VerifyHash

	var dstHash uint64
	err = e.step(ctx, name, "verify", func() error {
		var err error
		dstHash, err = verify.Transferred(ctx, e.cfg.Verify, c.dst, tmp, sent, srcHash)
		return err
	})
	if err != nil {
		e.discard(ctx, c.dst, tmp)
		return fail(err)
	}
	res.DstHash = dstHash

	var skipped bool
	err = e.step(ctx, name, "rename", func() error {
		var err error
		skipped, err = e.publish(ctx, c.dst, tmp, name)
		return err
	})
	if err != nil {
		e.discard(ctx, c.dst, tmp)
		return fail(err)
	}
	if skipped {
		res.Outcome, res.Reason = Skipped, ReasonAlreadyExists
		return finish(res)
	}

	if err := e.postAction(ctx, c, name); err != nil {
		res.postActionFailed = true
		return fail(err)
	}

	res.Outcome = Success
	return finish(res)
}

// existingDecision is what to do about a destination file that is already
// there.
type existingDecision int

const (
	// proceed means transfer the file: either nothing is in the way, or what is
	// there differs from the source.
	proceed existingDecision = iota
	// skipExisting leaves an existing file alone without looking at it.
	skipExisting
	// skipIdentical means the destination already holds these bytes.
	skipIdentical
)

// decideExisting settles what to do about a name that is already taken.
//
// The answer comes from the listing taken at the start of the cycle rather than
// from a Stat per file, which is what keeps a directory of already-transferred
// files from costing a round trip each on every pass.
func (e *Engine) decideExisting(ctx context.Context, c *conn, t target, idx *destIndex, dir, base string) (existingDecision, error) {
	dstSize, exists := idx.lookup(dir, base)
	if !exists {
		return proceed, nil
	}
	if e.cfg.OnExists == config.OnExistsSkip {
		return skipExisting, nil
	}

	name := t.file.Path
	var identical bool
	err := e.step(ctx, name, "compare", func() error {
		var err error
		identical, err = verify.Identical(ctx, e.cfg.Verify, c.src, name, t.file.Size, c.dst, name, dstSize)
		return err
	})
	switch {
	case err != nil:
		return proceed, err
	case identical:
		return skipIdentical, nil
	default:
		return proceed, nil
	}
}

// upload streams the file to its temporary name, hashing what it sends.
//
// The digest has to come from the bytes actually streamed. Reusing one computed
// while comparing against an existing file would verify what was read then, not
// what was sent now.
func (e *Engine) upload(ctx context.Context, c *conn, name, tmp string) (sent int64, srcHash uint64, err error) {
	err = e.step(ctx, name, "write", func() error {
		rc, err := c.src.Open(ctx, name)
		if err != nil {
			return err
		}
		defer rc.Close()

		var reader io.Reader = rc
		digest := xxhash.New()
		if e.cfg.Verify == config.VerifyHash {
			reader = io.TeeReader(rc, digest)
		}
		sent, err = c.dst.Write(ctx, tmp, reader)
		srcHash = digest.Sum64()
		return err
	})
	return sent, srcHash, err
}

// publish moves the verified temporary file onto its final name.
//
// The destination is checked once more here because the index is a snapshot
// taken at the start of the cycle: another process may have created the file in
// the meantime. Under on_exists: skip that file is left alone rather than
// silently overwritten.
func (e *Engine) publish(ctx context.Context, dst fsys.FS, tmp, name string) (skipped bool, err error) {
	if e.cfg.OnExists == config.OnExistsSkip {
		switch _, err := dst.Stat(ctx, name); {
		case err == nil:
			e.discard(ctx, dst, tmp)
			return true, nil
		case !errors.Is(err, fs.ErrNotExist):
			return false, err
		}
	}
	// Rename first: where the server replaces the target atomically there is
	// never a moment with no file at all, and nothing is spent on a Remove for
	// the common case of a name that is not taken.
	err = dst.Rename(ctx, tmp, name)
	if err == nil {
		return false, nil
	}
	// Servers that refuse to rename onto an existing name need it cleared.
	if rmErr := dst.Remove(ctx, name); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
		return false, err
	}
	return false, dst.Rename(ctx, tmp, name)
}

// postAction tidies up the sending side once the file has arrived.
//
// It retries on its own rather than letting the caller repeat the transfer: the
// file is already at the destination, so re-sending it would achieve nothing.
func (e *Engine) postAction(ctx context.Context, c *conn, name string) error {
	if e.cfg.PostAction == config.PostNone || e.cfg.PostAction == "" {
		return nil
	}

	var err error
	for n := 1; ; n++ {
		err = e.step(ctx, name, "postaction", func() error {
			return postact.Apply(ctx, e.cfg.PostAction, c.src, name, e.cfg.MoveTo)
		})
		if err == nil || n >= e.cfg.Retry.MaxAttempts || !Retryable(err) {
			return err
		}
		// The sending side may be what went away, so rebuild it too.
		c.invalidate()
		if !e.pause(ctx, n, name, err) {
			return err
		}
		if cerr := c.ensure(ctx); cerr != nil {
			return cerr
		}
	}
}

// discard removes a temporary file, reporting failure only at warn level: the
// transfer result itself has already been decided by the caller.
func (e *Engine) discard(ctx context.Context, dst fsys.FS, tmp string) {
	if err := dst.Remove(ctx, tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		e.log.Warn("could not remove temporary file",
			logging.KeyEvent, logging.EventTransfer,
			logging.KeyDst, e.destination(tmp), logging.KeyError, err.Error())
	}
}

// step runs one stage of a transfer, timing it for the debug breakdown.
func (e *Engine) step(ctx context.Context, name, stage string, fn func() error) error {
	if !e.log.Enabled(ctx, slog.LevelDebug) {
		return fn()
	}
	t0 := e.now()
	err := fn()
	attrs := []any{
		logging.KeyEvent, logging.EventTransfer,
		logging.KeyStep, stage,
		logging.KeySrc, e.source(name),
		logging.KeyDurationMS, e.now().Sub(t0).Milliseconds(),
	}
	if err != nil {
		attrs = append(attrs, logging.KeyError, err.Error())
	}
	e.log.Debug("step", attrs...)
	return err
}

// report folds a result into the summary, logs it and hands it to the console.
//
// Workers run in parallel, so this is serialised: callers of the engine get
// one result at a time, and the log line and console line for a file stay
// together.
func (e *Engine) report(ctx context.Context, c *Collector, r Result) {
	e.reportMu.Lock()
	defer e.reportMu.Unlock()

	c.Add(r)

	// Which parameters a transfer record carries is configuration; the level
	// only decides which records are written at all.
	attrs := []any{logging.KeyEvent, logging.EventTransfer}
	add := func(field string, value any) {
		if e.fields.Has(field) {
			attrs = append(attrs, field, value)
		}
	}

	add(logging.KeySrc, e.source(r.Path))
	add(logging.KeyDst, e.destination(r.Path))
	add(logging.KeyProtocol, string(e.cfg.Remote.Protocol))
	add(logging.KeyBytes, r.Bytes)
	add(logging.KeyDurationMS, r.Elapsed.Milliseconds())
	add(logging.KeyVerify, string(e.cfg.Verify))
	add(logging.KeyResult, string(r.Outcome))
	if r.Reason != "" {
		add(logging.KeyReason, string(r.Reason))
	}
	if r.Err != nil {
		add(logging.KeyError, r.Err.Error())
	}
	if r.Hashed {
		// The two digests are the evidence that the file arrived intact.
		add(logging.KeyHashSrc, verify.Format(r.SrcHash))
		add(logging.KeyHashDst, verify.Format(r.DstHash))
	}
	if r.Attempts > 1 {
		add(logging.KeyAttempt, r.Attempts)
	}
	add(logging.KeyRateMiBs, rate(r.Bytes, r.Elapsed))

	e.log.Log(ctx, r.Level(), "transfer", attrs...)

	if e.opts.OnResult != nil {
		e.opts.OnResult(r)
	}
}

// source and destination render a file's two ends in full.
//
// The engine works in paths relative to each root, which is what keeps it
// direction agnostic, but a log read weeks later has to say which file on which
// machine, so the records carry the whole location.
func (e *Engine) source(rel string) string {
	if e.opts.Direction == config.DirRecv {
		return e.remotePath(rel)
	}
	return e.localPath(rel)
}

func (e *Engine) destination(rel string) string {
	if e.opts.Direction == config.DirRecv {
		return e.localPath(rel)
	}
	return e.remotePath(rel)
}

func (e *Engine) localPath(rel string) string {
	return filepath.Join(e.cfg.Local.Path, filepath.FromSlash(rel))
}

func (e *Engine) remotePath(rel string) string {
	return e.cfg.Remote.Describe() + "/" + rel
}

func rate(bytes int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(bytes) / (1024 * 1024) / d.Seconds()
}
