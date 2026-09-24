// Package cmd wires the command line onto the transfer engine.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"goft/internal/config"
	"goft/internal/console"
	"goft/internal/engine"
	"goft/internal/fsys"
	"goft/internal/logging"
)

var (
	flagConfig    string
	flagLogLevel  string
	flagLogFile   string
	flagConsole   bool
	flagNoConsole bool
)

// exitError carries the process exit code alongside the failure.
//
// The codes let monitoring tell "the job ran but some files failed" (1) apart
// from "the job never got going" (2).
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// startupError marks a failure that prevented the job from running.
func startupError(err error) error { return &exitError{code: 2, err: err} }

// transferError marks a run that completed with failed files.
func transferError(n int) error {
	return &exitError{code: 1, err: fmt.Errorf("%d file(s) failed", n)}
}

var rootCmd = &cobra.Command{
	Use:           "goft",
	Short:         "Transfer files between a local directory and an FTP, SFTP or SMB server",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&flagConfig, "config", "c", "", "path to the job configuration file")
	pf.StringVar(&flagLogLevel, "log-level", "", "override log.level (debug | info | warn | error)")
	pf.StringVar(&flagLogFile, "log-file", "", "override log.path")
	pf.BoolVar(&flagConsole, "console", false, "force human readable output on stdout")
	pf.BoolVar(&flagNoConsole, "no-console", false, "suppress human readable output on stdout")
	rootCmd.MarkFlagsMutuallyExclusive("console", "no-console")
}

// Execute runs the command line and returns the process exit code.
func Execute() int {
	err := rootCmd.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, "goft: "+err.Error())

	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 2
}

// findConfig locates the job configuration.
//
// The search deliberately goes through os.UserConfigDir rather than a
// hard-coded path, so the same binary behaves sensibly on every platform.
func findConfig() (string, error) {
	if flagConfig != "" {
		return flagConfig, nil
	}
	candidates := []string{"goft.yaml"}
	if dir, err := os.UserConfigDir(); err == nil {
		candidates = append(candidates, filepath.Join(dir, "goft", "goft.yaml"))
	}
	if runtime.GOOS != "windows" {
		candidates = append(candidates, "/etc/goft/goft.yaml")
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("no configuration found; looked at %v (use --config)", candidates)
}

// job holds everything one command needs to run.
type job struct {
	cfg      *config.Config
	log      *slog.Logger
	closeLog io.Closer
	console  *console.Console
	engine   *engine.Engine
}

// loadConfig reads and validates the configuration for a direction.
func loadConfig(dir config.Direction) (*config.Config, error) {
	path, err := findConfig()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration %s:\n%w", path, err)
	}
	if err := config.ValidateForDirection(cfg, dir); err != nil {
		return nil, fmt.Errorf("invalid configuration %s: %w", path, err)
	}
	if flagLogFile != "" {
		if abs, err := filepath.Abs(flagLogFile); err == nil {
			cfg.Log.Path = abs
		} else {
			cfg.Log.Path = flagLogFile
		}
	}
	if flagLogLevel != "" {
		if _, err := config.ParseLevel(flagLogLevel); err != nil {
			return nil, fmt.Errorf("--log-level: %w", err)
		}
		cfg.Log.Level = flagLogLevel
	}
	return cfg, nil
}

// consoleEnabled decides whether stdout carries a human readable account.
//
// One-shot runs are watched by a person, so they default to on; a daemon writes
// to its log instead. A dry run exists to be read, so it is always on.
func consoleEnabled(single, dryRun bool) bool {
	switch {
	case dryRun:
		return true
	case flagNoConsole:
		return false
	case flagConsole:
		return true
	default:
		return single
	}
}

// newJob assembles configuration, logger, console and engine.
func newJob(dir config.Direction, single, dryRun bool) (*job, error) {
	cfg, err := loadConfig(dir)
	if err != nil {
		return nil, err
	}
	level, err := config.ParseLevel(cfg.Log.Level)
	if err != nil {
		return nil, err
	}
	withConsole := consoleEnabled(single, dryRun)

	log, closer, err := logging.New(logging.Options{
		Log:            cfg.Log,
		Level:          level,
		Job:            cfg.Name,
		Direction:      dir,
		ConsoleEnabled: withConsole,
	})
	if err != nil {
		return nil, err
	}
	for _, w := range config.Warnings(cfg) {
		log.Warn(w, logging.KeyEvent, logging.EventLifecycle)
	}
	if err := reportResolution(log, cfg.Remote); err != nil {
		return nil, err
	}

	newLocal := func(context.Context) (fsys.FS, error) {
		return fsys.NewLocal(cfg.Local.Path), nil
	}
	newRemote := func(ctx context.Context) (fsys.FS, error) {
		return fsys.NewRemote(ctx, cfg.Remote)
	}
	newSrc, newDst := newLocal, newRemote
	if dir == config.DirRecv {
		newSrc, newDst = newRemote, newLocal
	}

	j := &job{cfg: cfg, log: log, closeLog: closer}

	opts := engine.Options{
		Config:    cfg,
		Direction: dir,
		NewSrc:    newSrc,
		NewDst:    newDst,
		Logger:    log,
		DryRun:    dryRun,
		Single:    single,
	}
	if withConsole {
		j.console = console.New(console.Options{
			Writer:    os.Stdout,
			Level:     level,
			Color:     term.IsTerminal(int(os.Stdout.Fd())),
			Job:       cfg.Name,
			Direction: dir,
			Local:     cfg.Local.Path,
			Remote:    cfg.Remote.Describe(),
			Buffered:  !single,
		})
		opts.OnPlan = j.console.Plan
		opts.OnResult = j.console.Result
		opts.OnSummary = j.console.Summary
		opts.OnCycleError = j.console.CycleError
	}
	j.engine = engine.New(opts)
	return j, nil
}

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
	for _, w := range resolved.Warnings {
		log.Warn(w, logging.KeyEvent, logging.EventConnect)
	}
	for _, t := range resolved.Trace {
		log.Debug("resolved connection setting",
			logging.KeyEvent, logging.EventConnect,
			"field", t.Field, "value", t.Value, "source", t.Source)
	}
	return nil
}

// logConfiguration records the settings the run is about to use.
//
// It is written once, at info level, so that a log kept for auditing answers
// "what settings moved this file" on its own, months later, without the
// configuration file having to still exist in that form.
func (j *job) logConfiguration() {
	j.log.Info("starting",
		logging.KeyEvent, logging.EventLifecycle,
		"config", j.cfg)
}

func (j *job) close() {
	if j.closeLog != nil {
		_ = j.closeLog.Close()
	}
}

// runOnce performs a single cycle and maps the outcome onto an exit code.
func runOnce(ctx context.Context, dir config.Direction, dryRun bool) error {
	j, err := newJob(dir, true, dryRun)
	if err != nil {
		return startupError(err)
	}
	defer j.close()

	j.logConfiguration()
	summary, err := j.engine.RunOnce(ctx)
	if err != nil {
		j.log.Error("run failed", logging.KeyEvent, logging.EventLifecycle, logging.KeyError, err.Error())
		return startupError(err)
	}
	if summary.Failed > 0 {
		return transferError(summary.Failed)
	}
	return nil
}

// runServe polls until the process is asked to stop.
//
// A dry run reports once and stops: there is nothing to watch for when nothing
// is going to be transferred.
func runServe(ctx context.Context, dir config.Direction, dryRun bool) error {
	if dryRun {
		return runOnce(ctx, dir, true)
	}

	j, err := newJob(dir, false, dryRun)
	if err != nil {
		return startupError(err)
	}
	defer j.close()

	j.logConfiguration()
	if err := j.engine.Serve(ctx); err != nil {
		return startupError(err)
	}
	j.log.Info("stopped", logging.KeyEvent, logging.EventLifecycle)
	return nil
}
