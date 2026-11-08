// Package cmd wires the command line onto the transfer engine.
package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"goft/internal/config"
	"goft/internal/i18n"
)

var (
	flagConfig    string
	flagLogLevel  string
	flagConsole   bool
	flagNoConsole bool
	flagRunOnce   []string
	flagDryRun    bool
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

// startupError marks a failure that prevented a job, or the service, from
// running.
func startupError(err error) error { return &exitError{code: 2, err: err} }

var rootCmd = &cobra.Command{
	Use:   "goft",
	Short: i18n.N("Transfer files between a local directory and an FTP, SFTP or SMB server, or another directory"),
	Long: i18n.N("Transfer files between a local directory and an FTP, SFTP or SMB server, or another directory.\n\n" +
		"Without a command, goft runs every enabled job in its configuration and keeps\n" +
		"watching until it is stopped. --run-once runs the named jobs once instead."),
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		once := len(flagRunOnce) > 0 || flagDryRun
		if !once && underServiceManager() {
			return runUnderServiceManager()
		}
		ctx, stop := signalContext(cmd.Context())
		defer stop()
		cfg, err := loadConfig()
		if err != nil {
			return startupError(err)
		}
		if once {
			return runOnce(ctx, cfg, flagRunOnce, flagDryRun)
		}
		return runService(ctx, cfg, nil)
	},
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
	pf.StringVarP(&flagConfig, "config", "c", "", i18n.N("path to the configuration file"))
	pf.StringVar(&flagLogLevel, "log-level", "", i18n.N("override log.level of the service and every job (debug | info | warn | error)"))
	pf.BoolVar(&flagConsole, "console", false, i18n.N("force human readable output on stdout"))
	pf.BoolVar(&flagNoConsole, "no-console", false, i18n.N("suppress human readable output on stdout"))
	pf.StringVar(&flagLang, "lang", "", i18n.N("language to write in, en or ja (default: from GOFT_LANG or the locale)"))
	rootCmd.MarkFlagsMutuallyExclusive("console", "no-console")

	f := rootCmd.Flags()
	f.StringArrayVar(&flagRunOnce, "run-once", nil, i18n.N("run the named job once and exit; repeat it to run several, one after another"))
	f.BoolVar(&flagDryRun, "dry-run", false, i18n.N("list what would be transferred without transferring anything; with no --run-once, for every enabled job"))

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

// findConfig locates the configuration.
//
// The search deliberately goes through os.UserConfigDir rather than a
// hard-coded path, so the same binary behaves sensibly on every platform. A
// service on Windows runs as an account whose own configuration directory
// nobody looks in, so ProgramData is searched there too.
func findConfig() (string, error) {
	if flagConfig != "" {
		return flagConfig, nil
	}
	candidates := []string{"goft.yaml"}
	if dir, err := os.UserConfigDir(); err == nil {
		candidates = append(candidates, filepath.Join(dir, "goft", "goft.yaml"))
	}
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("ProgramData"); dir != "" {
			candidates = append(candidates, filepath.Join(dir, "goft", "goft.yaml"))
		}
	} else {
		candidates = append(candidates, "/etc/goft/goft.yaml")
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", i18n.Errorf("no configuration found; looked at %v (use --config)", candidates)
}

// loadConfig reads and validates the configuration, every job in it.
func loadConfig() (*config.Config, error) {
	path, err := findConfig()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	if flagLogLevel != "" {
		if _, err := config.ParseLevel(flagLogLevel); err != nil {
			return nil, i18n.Errorf("--log-level: %w", err)
		}
		cfg.Service.Log.Level = flagLogLevel
		for _, j := range cfg.Jobs {
			j.Log.Level = flagLogLevel
		}
	}
	if err := config.Validate(cfg); err != nil {
		return nil, i18n.Errorf("invalid configuration %s:\n%w", cfg.SourceFile, err)
	}
	return cfg, nil
}

// consoleEnabled decides whether stdout carries a human readable account.
//
// A single run is watched by a person, so it defaults to on; a service writes
// to its logs instead. A dry run exists to be read, so it is always on.
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
