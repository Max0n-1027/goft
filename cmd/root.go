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
	"goft/internal/i18n"
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
	return &exitError{code: 1, err: i18n.Errorf("%d file(s) failed", n)}
}

var rootCmd = &cobra.Command{
	Use:           "goft",
	Short:         i18n.N("Transfer files between a local directory and an FTP, SFTP or SMB server, or another directory"),
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	// The language is settled once the flags are read and before any command
	// runs. This is set here rather than in the literal above because it
	// reaches back to rootCmd.
	rootCmd.PersistentPreRunE = func(*cobra.Command, []string) error {
		if err := applyLanguage(); err != nil {
			return startupError(err)
		}
		return nil
	}

	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&flagConfig, "config", "c", "", i18n.N("path to the job configuration file"))
	pf.StringVar(&flagLogLevel, "log-level", "", i18n.N("override log.level (debug | info | warn | error)"))
	pf.StringVar(&flagLogFile, "log-file", "", i18n.N("override log.path"))
	pf.BoolVar(&flagConsole, "console", false, i18n.N("force human readable output on stdout"))
	pf.BoolVar(&flagNoConsole, "no-console", false, i18n.N("suppress human readable output on stdout"))
	pf.StringVar(&flagLang, "lang", "", i18n.N("language to write in, en or ja (default: from GOFT_LANG or the locale)"))
	rootCmd.MarkFlagsMutuallyExclusive("console", "no-console")

	// Help is shown without running the command, so it settles the language
	// itself; by the time it is called the flags, --lang among them, are read.
	help := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		_ = applyLanguage()
		help(c, args)
	})
}

// Execute runs the command line and returns the process exit code.
func Execute() int {
	err := rootCmd.Execute()
	if err == nil {
		return 0
	}
	// A command line cobra could not parse never reached the point where the
	// language is settled, so settle it now to report the error in it.
	_ = applyLanguage()
	fmt.Fprintln(os.Stderr, "goft: "+tr.Error(err))

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
	return "", i18n.Errorf("no configuration found; looked at %v (use --config)", candidates)
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
		return nil, i18n.Errorf("invalid configuration %s:\n%w", path, err)
	}
	if err := config.ValidateForDirection(cfg, dir); err != nil {
		return nil, i18n.Errorf("invalid configuration %s: %w", path, err)
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
			return nil, i18n.Errorf("--log-level: %w", err)
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
	j := &job{cfg: cfg, log: log, closeLog: closer}

	// The settings open the log, so that a warning about them is read after
	// them rather than before.
	j.logConfiguration()
	for _, w := range config.Warnings(cfg) {
		log.Warn(w.String(), logging.KeyEvent, logging.EventLifecycle)
	}
	if err := reportResolution(log, cfg.Remote); err != nil {
		j.close()
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
			Printer:   tr,
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

	summary, err := j.engine.RunOnce(ctx)
	switch {
	case err != nil && ctx.Err() != nil:
		// Stopped on request. The files under way were finished; the cycle's
		// own summary, if it got that far, says what was left.
		j.log.Warn("interrupted", logging.KeyEvent, logging.EventLifecycle)
		return startupError(i18n.Errorf("interrupted before the run was complete"))
	case err != nil:
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

	if err := j.engine.Serve(ctx); err != nil {
		return startupError(err)
	}
	j.log.Info("stopped", logging.KeyEvent, logging.EventLifecycle)
	return nil
}
