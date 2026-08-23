package engine

import (
	"context"
	"log/slog"
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
		opts:   opts,
		cfg:    opts.Config,
		filter: scan.NewFilter(opts.Config.Include, opts.Config.Exclude),
		stab:   scan.NewStabilizer(opts.Config.StableDuration, now),
		base:   log,
		log:    log,
		fields: opts.Config.LogFields(level),
		now:    now,
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

	// The scanning connection becomes worker 0's, so the cycle never holds more
	// than `workers` connections per side.
	dst, err := e.opts.NewDst(ctx)
	if err != nil {
		return Summary{}, err
	}
	srcOpen = false

	conns, err := e.openConnections(ctx, src, dst, len(targets))
	defer func() {
		for _, c := range conns {
			c.close()
		}
	}()
	if err != nil {
		return Summary{}, err
	}

	idx, err := buildDestIndex(ctx, dst, targetDirs(targets))
	if err != nil {
		return Summary{}, err
	}

	s, err := e.dispatch(ctx, conns, targets, idx)
	if err != nil {
		return s, err
	}
	s.Elapsed = e.now().Sub(start)
	e.log.Info("cycle complete",
		logging.KeyEvent, logging.EventSummary,
		"files", s.Total, "succeeded", s.Succeeded, "skipped", s.Skipped, "failed", s.Failed,
		logging.KeyBytes, s.Bytes, logging.KeyDurationMS, s.Elapsed.Milliseconds())
	if e.opts.OnSummary != nil {
		e.opts.OnSummary(s)
	}
	return s, nil
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
func (e *Engine) dispatch(ctx context.Context, conns []*conn, targets []target, idx *destIndex) (Summary, error) {
	collector := &Collector{}
	jobs := make(chan target)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		defer close(jobs)
		for i, t := range targets {
			t.index, t.total = i+1, len(targets)
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

	err := g.Wait()
	return collector.Summary(), err
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
// carried along so that they appear in the plan and in the summary.
type target struct {
	file         scan.File
	overSizeCap  bool
	recurring    bool
	index, total int
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
	for _, f := range stable {
		t := target{file: f}
		if limit > 0 && f.Size > limit {
			t.overSizeCap = true
			// Reported at warn the first time and at debug on later cycles.
			// This is why the size limit is applied after settling rather than
			// during the scan: a file dropped during the scan would never reach
			// the stabilizer, and the suppression would have nothing to go on.
			t.recurring = !e.stab.ChangedInLastCycle(f.Path)
		}
		targets = append(targets, t)
	}
	e.log.Debug("scan complete",
		logging.KeyEvent, logging.EventScan,
		"found", len(files), "settled", len(stable))
	return targets, nil
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
