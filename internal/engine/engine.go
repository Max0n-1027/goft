package engine

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"goft/internal/config"
	"goft/internal/fsys"
	"goft/internal/logging"
	"goft/internal/scan"
)

// Options wires an engine to a job, a direction and its outputs.
type Options struct {
	// Config is the job, and Direction which way it runs.
	Config    *config.Config
	Direction config.Direction

	// NewSrc and NewDst open one connection each. They are called once per
	// worker, so the number of live connections never exceeds workers.
	NewSrc func(context.Context) (fsys.FS, error)
	NewDst func(context.Context) (fsys.FS, error)

	// Logger records what happened. When nil the default logger is used.
	Logger *slog.Logger

	// OnPlan is called once per cycle before any transfer, OnResult once per
	// file, and OnSummary once the cycle is complete. They exist for the
	// console; the engine itself does not care whether anyone is listening.
	//
	// Files are transferred in parallel, but these calls are serialised, so an
	// implementation needs no locking of its own.
	OnPlan    func(Plan)
	OnResult  func(Result)
	OnSummary func(Summary)
	// OnCycleError reports a cycle that could not run at all, which serve
	// otherwise only records in the log.
	OnCycleError func(error)

	// DryRun reports what would be transferred without touching the receiving
	// side at all.
	DryRun bool

	// Single marks a one-shot run (send/recv). Without a previous cycle to
	// compare against, settling has to be established by observing twice.
	Single bool

	// Now supplies the clock, so that settling can be tested without waiting.
	// When nil, time.Now is used.
	Now func() time.Time
}

// Engine performs transfer cycles for one job. Build one with [New] and drive
// it with [Engine.RunOnce] or [Engine.Serve].
type Engine struct {
	opts   Options
	cfg    *config.Config
	filter *scan.Filter
	stab   *scan.Stabilizer
	// base is the process logger; log is base with the current cycle's id
	// attached, so that every record a cycle produces carries it without the
	// id having to be threaded through each call.
	//
	// Cycles never overlap: log is written before a cycle's workers start and
	// only read while they run.
	base   *slog.Logger
	log    *slog.Logger
	fields config.LogFieldSet
	now    func() time.Time

	// oversize remembers the files already reported as too large to send, so
	// that the report is made once per file rather than once per cycle. Only
	// collect touches it, and cycles never overlap.
	oversize map[string]scan.File

	// reportMu serialises result reporting so that the collector, the log and
	// the console see one result at a time and in a consistent order.
	reportMu sync.Mutex
}

// New builds an engine. The stabilizer is kept across cycles, which is what
// lets serve accumulate the settle duration over several polls.
func New(opts Options) *Engine {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	// A bad level cannot reach here: the configuration is validated first.
	level, _ := config.ParseLevel(opts.Config.Log.Level)
	return &Engine{
		opts:     opts,
		cfg:      opts.Config,
		filter:   scan.NewFilter(opts.Config.Include, opts.Config.Exclude),
		stab:     scan.NewStabilizer(opts.Config.StableDuration, now),
		base:     log,
		log:      log,
		fields:   opts.Config.LogFields(level),
		now:      now,
		oversize: map[string]scan.File{},
	}
}

// Serve polls until ctx is cancelled.
//
// The loop is deliberately serial: the next cycle only starts once the previous
// one has finished, so a file being transferred can never be picked up a second
// time and no in-flight bookkeeping is needed.
func (e *Engine) Serve(ctx context.Context) error {
	failures := 0
	for {
		_, err := e.RunOnce(ctx)
		switch {
		case err == nil:
			failures = 0
		case ctx.Err() != nil:
			return nil
		default:
			// A cycle that could not start at all is logged and tried again
			// rather than killing the daemon.
			failures++
			wait := e.cycleWait(failures)
			e.log.Error("transfer cycle failed",
				logging.KeyEvent, logging.EventSummary,
				logging.KeyError, err.Error(),
				"consecutive_failures", failures,
				"retry_in_ms", wait.Milliseconds())
			if e.opts.OnCycleError != nil {
				e.opts.OnCycleError(err)
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(e.cycleWait(failures)):
		}
	}
}

// maxCycleBackoff bounds how far apart cycles drift while they keep failing.
const maxCycleBackoff = 5 * time.Minute

// cycleWait is the pause before the next cycle.
//
// While cycles are failing outright — an unreachable server, say — the pause
// doubles. A job polling every second against a server that has gone for good
// would otherwise write tens of thousands of identical errors a day and hammer
// the network doing it. The interval returns to normal the moment a cycle
// succeeds, so a brief outage costs nothing.
//
// The pause never drops below poll_interval, and never grows past
// maxCycleBackoff unless poll_interval is already longer than that.
func (e *Engine) cycleWait(failures int) time.Duration {
	wait := e.cfg.PollInterval
	cap := maxCycleBackoff
	if wait > cap {
		cap = wait
	}
	for i := 0; i < failures && wait < cap; i++ {
		wait *= 2
	}
	if wait > cap {
		return cap
	}
	return wait
}

// RunOnce performs a single cycle.
//
// Every record the cycle produces carries a fresh cycle id, so that one pass
// can be picked out of a log that a watching process has been appending to for
// days.
func (e *Engine) RunOnce(ctx context.Context) (Summary, error) {
	start := e.now()
	e.log = e.base.With(logging.KeyCycleID, uuid.NewString())

	src, err := e.opts.NewSrc(ctx)
	if err != nil {
		return Summary{}, err
	}
	srcOpen := true
	defer func() {
		if srcOpen {
			_ = src.Close()
		}
	}()

	targets, err := e.collect(ctx, src)
	if err != nil {
		return Summary{}, err
	}

	plan := Plan{DryRun: e.opts.DryRun}
	for _, t := range targets {
		plan.Files = append(plan.Files, t.file)
		plan.Bytes += t.file.Size
	}
	if e.opts.OnPlan != nil {
		e.opts.OnPlan(plan)
	}

	if e.opts.DryRun {
		s := Summary{Total: len(targets), Bytes: plan.Bytes, Elapsed: e.now().Sub(start), PlannedOnly: true}
		e.log.Info("dry run complete",
			logging.KeyEvent, logging.EventSummary, "files", s.Total, logging.KeyBytes, s.Bytes)
		if e.opts.OnSummary != nil {
			e.opts.OnSummary(s)
		}
		return s, nil
	}

	if len(targets) == 0 {
		e.log.Debug("nothing to transfer", logging.KeyEvent, logging.EventScan)
		return Summary{}, nil
	}

	// Files the size cap turns away are reported here, before anything is
	// opened. They need nothing from the destination, and a file that will
	// never be sent can sit in the source directory for weeks: a watcher
	// connecting on every poll just to say so again was pure cost.
	collector := &Collector{}
	sendable := make([]target, 0, len(targets))
	for i, t := range targets {
		t.index, t.total = i+1, len(targets)
		if t.overSizeCap {
			e.report(ctx, collector, Result{
				Index: t.index, Total: t.total, Path: t.file.Path, Bytes: t.file.Size,
				Outcome: Skipped, Reason: ReasonSizeLimit, Recurring: t.recurring,
			})
			continue
		}
		sendable = append(sendable, t)
	}
	if len(sendable) == 0 {
		return e.finishCycle(start, collector.Summary()), nil
	}

	// The scanning connection becomes worker 0's, so the cycle never holds more
	// than `workers` connections per side.
	dst, err := e.opts.NewDst(ctx)
	if err != nil {
		return Summary{}, err
	}
	srcOpen = false

	conns, err := e.openConnections(ctx, src, dst, len(sendable))
	defer func() {
		for _, c := range conns {
			c.close()
		}
	}()
	if err != nil {
		return Summary{}, err
	}

	idx, err := buildDestIndex(ctx, dst, targetDirs(sendable))
	if err != nil {
		return Summary{}, err
	}
	if idx.caseInsensitive {
		markCaseCollisions(sendable)
	}

	s, err := e.dispatch(ctx, conns, sendable, idx, collector)
	if err != nil {
		return s, err
	}
	return e.finishCycle(start, s), nil
}

// finishCycle records the summary of a cycle that ran to the end.
func (e *Engine) finishCycle(start time.Time, s Summary) Summary {
	s.Elapsed = e.now().Sub(start)
	summary := []any{
		logging.KeyEvent, logging.EventSummary,
		"files", s.Total, "succeeded", s.Succeeded, "skipped", s.Skipped, "failed", s.Failed,
		logging.KeyBytes, s.Bytes, logging.KeyDurationMS, s.Elapsed.Milliseconds(),
	}
	if s.DirsRemoved > 0 {
		// Only when it happened: a job that does not prune should not carry a
		// zero for it in every summary it ever writes.
		summary = append(summary, "dirs_removed", s.DirsRemoved)
	}
	e.log.Info("cycle complete", summary...)
	if e.opts.OnSummary != nil {
		e.opts.OnSummary(s)
	}
	return s
}

// openConnections gives each worker its own pair, reusing the two the cycle
// already holds for scanning and for the destination listing.
//
// There is no point in more workers than files, and the count is what bounds
// how many connections a cycle holds open at once.
func (e *Engine) openConnections(ctx context.Context, src, dst fsys.FS, targets int) ([]*conn, error) {
	workers := min(e.cfg.Workers, targets)
	conns := []*conn{{e: e, src: src, dst: dst}}

	for i := 1; i < workers; i++ {
		s, err := e.opts.NewSrc(ctx)
		if err != nil {
			return conns, err
		}
		d, err := e.opts.NewDst(ctx)
		if err != nil {
			_ = s.Close()
			return conns, err
		}
		conns = append(conns, &conn{e: e, src: s, dst: d})
	}
	return conns, nil
}

// dispatch hands the targets to the workers and waits for them to finish.
//
// A file that fails is reported rather than returned, so one bad file does not
// end the cycle; only a cancelled run or a connection that could not be rebuilt
// comes back as an error.
func (e *Engine) dispatch(ctx context.Context, conns []*conn, targets []target, idx *destIndex, collector *Collector) (Summary, error) {
	jobs := make(chan target)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		defer close(jobs)
		for _, t := range targets {
			select {
			case jobs <- t:
			case <-gctx.Done():
				return gctx.Err()
			}
		}
		return nil
	})
	for _, c := range conns {
		g.Go(func() error {
			for t := range jobs {
				if err := gctx.Err(); err != nil {
					return err
				}
				e.report(gctx, collector, e.attempt(gctx, c, t, idx))
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return collector.Summary(), err
	}

	summary := collector.Summary()
	summary.DirsRemoved = e.pruneEmptied(ctx, conns[0], collector.Emptied())
	return summary, nil
}

// pruneEmptied removes the sending side's directories that this cycle took the
// last file out of.
//
// Only directories a file was actually moved out of are considered, and only
// while they are still empty when the time comes: a directory that was already
// empty before the cycle, or that something else has written to since, is left
// alone. The sending root is never a candidate, since a job whose own directory
// disappears has nothing to watch.
//
// Failing to remove one is a warning rather than a failure. The files reached
// the destination, which is the job; a directory that could not be tidied away
// will be tried again next cycle.
func (e *Engine) pruneEmptied(ctx context.Context, c *conn, dirs []string) int {
	if !e.cfg.RemoveEmptyDirs || e.cfg.PostAction == config.PostNone || len(dirs) == 0 {
		return 0
	}
	if err := c.ensure(ctx); err != nil {
		e.log.Warn("could not reach the sending side to remove empty directories",
			logging.KeyEvent, logging.EventPostAction, logging.KeyError, err.Error())
		return 0
	}

	removed := 0
	for _, dir := range dirs {
		entries, err := c.src.List(ctx, dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// Something else removed it first, which is the outcome anyway.
			continue
		case err != nil:
			e.log.Warn("could not list a directory to see whether it is empty",
				logging.KeyEvent, logging.EventPostAction,
				logging.KeySrc, e.source(dir), logging.KeyError, err.Error())
			continue
		case len(entries) > 0:
			continue
		}

		if err := c.src.Remove(ctx, dir); err != nil {
			e.log.Warn("could not remove an empty directory",
				logging.KeyEvent, logging.EventPostAction,
				logging.KeySrc, e.source(dir), logging.KeyError, err.Error())
			continue
		}
		removed++
		e.log.Info("removed empty directory",
			logging.KeyEvent, logging.EventPostAction, logging.KeySrc, e.source(dir))
	}
	return removed
}

// conn is one worker's pair of connections, which it can rebuild.
//
// Rebuilding matters for retrying: a dropped connection is the likeliest reason
// a transfer failed, and trying again over the same dead one would fail in the
// same way.
type conn struct {
	e   *Engine
	src fsys.FS
	dst fsys.FS
}

// ensure opens whatever is not currently connected.
func (c *conn) ensure(ctx context.Context) error {
	if c.src != nil && c.dst != nil {
		return nil
	}
	c.close()

	src, err := c.e.opts.NewSrc(ctx)
	if err != nil {
		return err
	}
	dst, err := c.e.opts.NewDst(ctx)
	if err != nil {
		_ = src.Close()
		return err
	}
	c.src, c.dst = src, dst
	return nil
}

// invalidate drops the connections so that the next ensure rebuilds them.
func (c *conn) invalidate() { c.close() }

func (c *conn) close() {
	if c.src != nil {
		_ = c.src.Close()
	}
	if c.dst != nil {
		_ = c.dst.Close()
	}
	c.src, c.dst = nil, nil
}

// target is a file the cycle will handle. Files rejected by the size limit are
// carried along so that they appear in the plan and in the summary, but are
// reported without being handed to a worker.
type target struct {
	file         scan.File
	overSizeCap  bool
	recurring    bool
	index, total int
	// collidesWith names another file of this cycle that the destination
	// would store under the same name, because it does not distinguish case.
	collidesWith string
}

// markCaseCollisions finds files that differ only in case, for a destination
// that does not tell them apart.
//
// A case sensitive source can hold A.csv and a.csv side by side; a Windows or
// macOS disk or an SMB share cannot. Sending both would leave whichever arrived
// last, report both as delivered, and with post_action delete or move take both
// sources away — one of the two files lost with nothing to show for it. Neither
// is sent: there is no telling which one the destination should end up with.
//
// Whole paths are compared, so Invoices/a.csv and invoices/a.csv collide too.
// Only files that are to be written are passed in: one the size cap turned away
// collides with nothing.
func markCaseCollisions(targets []target) {
	byFolded := map[string][]int{}
	for i, t := range targets {
		folded := strings.ToLower(t.file.Path)
		byFolded[folded] = append(byFolded[folded], i)
	}
	for _, group := range byFolded {
		if len(group) < 2 {
			continue
		}
		for _, i := range group {
			other := group[0]
			if other == i {
				other = group[1]
			}
			targets[i].collidesWith = targets[other].file.Path
		}
	}
}

// collect scans, waits for files to settle and applies the size limit.
func (e *Engine) collect(ctx context.Context, src fsys.FS) ([]target, error) {
	files, err := scan.Scan(ctx, src, e.cfg.Recursive, e.filter)
	if err != nil {
		return nil, err
	}
	stable := e.stab.Observe(files)

	// A one-shot run has no previous cycle to compare against, so it observes
	// twice with the settle duration in between. Nothing to wait for when there
	// are no candidates, or when settling is switched off.
	if e.opts.Single && e.cfg.StableDuration > 0 && len(files) > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(e.cfg.StableDuration):
		}
		files, err = scan.Scan(ctx, src, e.cfg.Recursive, e.filter)
		if err != nil {
			return nil, err
		}
		stable = e.stab.Observe(files)
	}

	limit := e.cfg.MaxFileSizeMB * 1024 * 1024
	targets := make([]target, 0, len(stable))
	seen := make(map[string]bool)
	for _, f := range stable {
		t := target{file: f}
		if limit > 0 && f.Size > limit {
			t.overSizeCap = true
			t.recurring = e.noteOversize(f)
			seen[f.Path] = true
		}
		targets = append(targets, t)
	}
	e.forgetOversize(seen)
	e.log.Debug("scan complete",
		logging.KeyEvent, logging.EventScan,
		"found", len(files), "settled", len(stable))
	return targets, nil
}

// noteOversize records a file that exceeds the size cap, and reports whether
// the same file has already been reported. The first sighting is written at
// warn and later ones at debug, so a file that will never be sent does not
// repeat the same warning on every poll.
//
// The stabilizer's "changed since the last cycle" flag cannot answer this. A
// settled file never has it: it is not settled the first time it is seen, and
// by the time it settles it is no longer new. Keying the suppression off that
// flag made the warning unreachable for any job with a stable_duration, which
// is every job that is not in a test.
func (e *Engine) noteOversize(f scan.File) bool {
	prev, reported := e.oversize[f.Path]
	e.oversize[f.Path] = f
	// A file that grew, shrank or was rewritten is news again: it is not the
	// file that was reported, and staying quiet would hide that this one is
	// not moving either.
	return reported && prev.Size == f.Size && prev.ModTime.Equal(f.ModTime)
}

// forgetOversize drops files that are no longer over the cap, so that one which
// comes back is reported again and the map does not grow without end.
func (e *Engine) forgetOversize(seen map[string]bool) {
	for path := range e.oversize {
		if !seen[path] {
			delete(e.oversize, path)
		}
	}
}

func targetDirs(targets []target) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, t := range targets {
		d := fsys.Dir(t.file.Path)
		if !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	return dirs
}
