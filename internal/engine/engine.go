package engine

import (
	"context"
	"log/slog"
	"sync"
	"time"

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
	for {
		if _, err := e.RunOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// A cycle that could not start (usually the connection) is logged
			// and retried on the next tick rather than killing the daemon.
			e.log.Error("transfer cycle failed",
				logging.KeyEvent, logging.EventSummary, logging.KeyError, err.Error())
			if e.opts.OnCycleError != nil {
				e.opts.OnCycleError(err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(e.cfg.PollInterval):
		}
	}
}

// RunOnce performs a single cycle.
func (e *Engine) RunOnce(ctx context.Context) (Summary, error) {
	start := e.now()

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
	conns := []*conn{{e: e, src: src, dst: dst}}
	defer func() {
		for _, c := range conns {
			c.close()
		}
	}()

	idx, err := buildDestIndex(ctx, dst, targetDirs(targets))
	if err != nil {
		return Summary{}, err
	}

	workers := e.cfg.Workers
	if workers > len(targets) {
		workers = len(targets)
	}
	for i := 1; i < workers; i++ {
		s, err := e.opts.NewSrc(ctx)
		if err != nil {
			return Summary{}, err
		}
		d, err := e.opts.NewDst(ctx)
		if err != nil {
			_ = s.Close()
			return Summary{}, err
		}
		conns = append(conns, &conn{e: e, src: s, dst: d})
	}

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

	if err := g.Wait(); err != nil {
		return collector.Summary(), err
	}

	s := collector.Summary()
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
