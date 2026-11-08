package cmd

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"

	"goft/internal/config"
	"goft/internal/console"
	"goft/internal/engine"
	"goft/internal/fsys"
	"goft/internal/i18n"
	"goft/internal/lock"
	"goft/internal/logging"
)

// signalContext cancels on Ctrl+C and on SIGTERM.
//
// Cancelling stops every job from starting anything new; a file already under
// way is finished, so that a stop never leaves more than a temporary file
// behind. One that has stalled is dropped after remote.io_timeout, which bounds
// how long a stop can take.
//
// syscall.SIGTERM is defined on Windows as well, so this compiles everywhere
// even though only os.Interrupt is ever delivered there.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// lockDir is where the jobs' locks are kept: service.lock_dir, or the
// default for this system.
func lockDir(cfg *config.Config) (string, error) {
	if cfg.Service.LockDir != "" {
		return cfg.Service.LockDir, nil
	}
	return lock.DefaultDir()
}

// session is what the jobs of one goft process share: the configuration, the
// places their logs go, and the directory their locks are kept in.
type session struct {
	cfg     *config.Config
	outputs *logging.Outputs
	locks   string
}

// running is one job, ready to run: locked, logging, connected to its engine.
type running struct {
	job     *config.Job
	log     *slog.Logger
	console *console.Console
	engine  *engine.Engine
	lock    *lock.Lock
}

// start prepares a job to run: takes its lock, opens its log and records the
// settings it is about to use, resolves its connection, and builds its engine.
//
// The lock comes first. A job another process is running is refused before
// anything is written to its log, which belongs to the process running it.
func (s *session) start(job *config.Job, single, dryRun bool) (*running, error) {
	l, err := lock.Acquire(s.locks, job.Name, config.PathKey(job.SourceFile))
	if err != nil {
		return nil, err
	}
	r := &running{job: job, lock: l}

	level, _ := config.ParseLevel(job.Log.Level)
	withConsole := consoleEnabled(single, dryRun)
	log, err := logging.New(logging.Options{
		Log:            job.Log,
		Level:          level,
		Job:            job.Name,
		Direction:      job.Direction,
		ConsoleEnabled: withConsole,
		Outputs:        s.outputs,
	})
	if err != nil {
		r.release()
		return nil, i18n.Errorf("job %q: %w", job.Name, err)
	}
	r.log = log

	// The settings open the job's log, so that a warning about them is read
	// after them rather than before.
	log.Info("starting", logging.KeyEvent, logging.EventLifecycle, "config", job)
	for _, w := range config.Warnings(job) {
		log.Warn(w.String(), logging.KeyEvent, logging.EventLifecycle)
	}
	if err := reportResolution(log, job.Remote); err != nil {
		log.Error("could not start", logging.KeyEvent, logging.EventLifecycle, logging.KeyError, err.Error())
		r.release()
		return nil, i18n.Errorf("job %q: %w", job.Name, err)
	}

	newLocal := func(context.Context) (fsys.FS, error) {
		return fsys.NewLocal(job.Local.Path), nil
	}
	newRemote := func(ctx context.Context) (fsys.FS, error) {
		return fsys.NewRemote(ctx, job.Remote)
	}
	newSrc, newDst := newLocal, newRemote
	if job.Direction == config.DirRecv {
		newSrc, newDst = newRemote, newLocal
	}

	opts := engine.Options{
		Job:    job,
		NewSrc: newSrc,
		NewDst: newDst,
		Logger: log,
		DryRun: dryRun,
		Single: single,
	}
	if withConsole {
		r.console = console.New(console.Options{
			Writer:    s.outputs.Stdout(),
			Printer:   tr,
			Level:     level,
			Color:     term.IsTerminal(int(os.Stdout.Fd())),
			Job:       job.Name,
			Direction: job.Direction,
			Local:     job.Local.Path,
			Remote:    job.Remote.Describe(),
			Buffered:  !single,
		})
		opts.OnPlan = r.console.Plan
		opts.OnResult = r.console.Result
		opts.OnSummary = r.console.Summary
		opts.OnCycleError = r.console.CycleError
	}
	r.engine = engine.New(opts)
	return r, nil
}

func (r *running) release() { _ = r.lock.Release() }

// reportResolution records how the connection settings were arrived at.
//
// Resolving here rather than waiting for the first connection means a bad
// ssh_config fails at startup, and it puts the warnings — an ignored ProxyJump,
// host key checking relaxed by the alias — into the log of every run instead of
// only into `goft test`.
func reportResolution(log *slog.Logger, remote config.Remote) error {
	resolved, err := fsys.Resolve(remote)
	if err != nil {
		return err
	}
	// The log is in English whatever language the console speaks.
	for _, w := range resolved.Warnings {
		log.Warn(w.String(), logging.KeyEvent, logging.EventConnect)
	}
	for _, t := range resolved.Trace {
		log.Debug("resolved connection setting",
			logging.KeyEvent, logging.EventConnect,
			"field", t.Field, "value", t.Display(i18n.English), "source", t.Source)
	}
	return nil
}

// runService runs every enabled job until ctx is cancelled. ready, if not nil,
// is called once every job has started.
//
// Every job starts or none does. A service that came up with some of its jobs
// missing would look healthy while files that should be moving were not, so a
// job that cannot start — a lock another process holds, a connection setting
// that cannot be resolved — stops the service starting at all, and every such
// job is reported together.
func runService(ctx context.Context, cfg *config.Config, ready func()) error {
	outputs := logging.NewOutputs(nil)
	defer outputs.Close()

	level, _ := config.ParseLevel(cfg.Service.Log.Level)
	svc, err := logging.New(logging.Options{
		Log:            cfg.Service.Log,
		Level:          level,
		ConsoleEnabled: consoleEnabled(false, false),
		Outputs:        outputs,
	})
	if err != nil {
		return startupError(err)
	}
	fail := func(err error) error {
		svc.Error("service could not start", logging.KeyEvent, logging.EventService, logging.KeyError, err.Error())
		return startupError(err)
	}

	jobs := cfg.Enabled()
	names := make([]string, len(jobs))
	for i, j := range jobs {
		names[i] = j.Name
	}
	locks, lockErr := lockDir(cfg)
	svc.Info("service starting", logging.KeyEvent, logging.EventService,
		"config", cfg.SourceFile, "job_files", cfg.JobFiles, "jobs", names, "lock_dir", locks)
	if lockErr != nil {
		return fail(lockErr)
	}
	if len(jobs) == 0 {
		return fail(i18n.Errorf("no job is enabled in %s", cfg.SourceFile))
	}

	sess := &session{cfg: cfg, outputs: outputs, locks: locks}
	var (
		runs []*running
		errs []error
	)
	for _, j := range jobs {
		r, err := sess.start(j, false, false)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		runs = append(runs, r)
	}
	if len(errs) > 0 {
		for _, r := range runs {
			r.log.Info("stopped", logging.KeyEvent, logging.EventLifecycle)
			r.release()
		}
		return fail(errors.Join(errs...))
	}

	var wg sync.WaitGroup
	for _, r := range runs {
		wg.Add(1)
		go func(r *running) {
			defer wg.Done()
			defer r.release()
			if err := r.engine.Serve(ctx); err != nil {
				r.log.Error("stopped on an error", logging.KeyEvent, logging.EventLifecycle, logging.KeyError, err.Error())
				return
			}
			r.log.Info("stopped", logging.KeyEvent, logging.EventLifecycle)
		}(r)
	}
	if ready != nil {
		ready()
	}

	<-ctx.Done()
	svc.Info("service stopping", logging.KeyEvent, logging.EventService)
	wg.Wait()
	svc.Info("service stopped", logging.KeyEvent, logging.EventService)
	return nil
}

// runOnce runs each named job once, one after another, and exits with the worst
// of their outcomes. With no names it runs every enabled job.
//
// A job that fails, or cannot start, does not keep the next from running: each
// was asked for, and the exit status and the message say which went wrong.
func runOnce(ctx context.Context, cfg *config.Config, names []string, dryRun bool) error {
	jobs, err := pick(cfg, names)
	if err != nil {
		return startupError(err)
	}
	locks, err := lockDir(cfg)
	if err != nil {
		return startupError(err)
	}

	sess := &session{cfg: cfg, outputs: logging.NewOutputs(nil), locks: locks}
	defer sess.outputs.Close()

	var (
		errs  []error
		worst int
	)
	for _, j := range jobs {
		code, err := sess.runJobOnce(ctx, j, dryRun)
		if err != nil {
			errs = append(errs, err)
			worst = max(worst, code)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &exitError{code: worst, err: errors.Join(errs...)}
}

// runJobOnce runs one job for a single cycle and returns its exit status.
func (s *session) runJobOnce(ctx context.Context, j *config.Job, dryRun bool) (int, error) {
	if ctx.Err() != nil {
		return 2, i18n.Errorf("job %q: not run, since the run was stopped", j.Name)
	}
	r, err := s.start(j, true, dryRun)
	if err != nil {
		return 2, err
	}
	defer r.release()

	summary, err := r.engine.RunOnce(ctx)
	switch {
	case err != nil && ctx.Err() != nil:
		// Stopped on request. The files under way were finished; the cycle's
		// own summary, if it got that far, says what was left.
		r.log.Warn("interrupted", logging.KeyEvent, logging.EventLifecycle)
		return 2, i18n.Errorf("job %q: interrupted before the run was complete", j.Name)
	case err != nil:
		r.log.Error("run failed", logging.KeyEvent, logging.EventLifecycle, logging.KeyError, err.Error())
		return 2, i18n.Errorf("job %q: %w", j.Name, err)
	case summary.Failed > 0:
		return 1, i18n.Errorf("job %q: %d file(s) failed", j.Name, summary.Failed)
	}
	return 0, nil
}

// pick returns the jobs named, in the order named and each once, or every
// enabled job when none are. A job switched off can still be run by name: that
// is what asking for it by name means. Asking for every enabled job when none
// is enabled is an error, as it is for the service, rather than a run that
// quietly does nothing.
func pick(cfg *config.Config, names []string) ([]*config.Job, error) {
	if len(names) == 0 {
		jobs := cfg.Enabled()
		if len(jobs) == 0 {
			return nil, i18n.Errorf("no job is enabled in %s", cfg.SourceFile)
		}
		return jobs, nil
	}
	var (
		jobs    []*config.Job
		unknown []error
		seen    = map[*config.Job]bool{}
	)
	for _, name := range names {
		j, ok := cfg.Job(name)
		switch {
		case !ok:
			unknown = append(unknown, i18n.Errorf("no job called %q is configured (goft jobs lists them)", name))
		case !seen[j]:
			// The same job named twice, in either case, runs once.
			seen[j] = true
			jobs = append(jobs, j)
		}
	}
	if len(unknown) > 0 {
		return nil, errors.Join(unknown...)
	}
	return jobs, nil
}
