package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/spf13/cobra"

	"goft/internal/config"
	"goft/internal/console"
	"goft/internal/fsys"
	"goft/internal/i18n"
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: i18n.N("Check the configuration and the connection without transferring anything"),
	Long: i18n.N("Check the configuration and the connection without transferring anything.\n\n" +
		"Both directions are reported, because the configuration file does not say\n" +
		"which one a job will be used for. A read-only account fails the send check\n" +
		"and still exits 0; only a remote that supports neither direction exits 2."),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runTest(cmd.Context(), cmd.OutOrStdout())
	},
}

func init() { rootCmd.AddCommand(testCmd) }

// The checks goft test reports, and how each turned out. They are marked
// here and translated as they are printed.
var (
	checkConfig  = i18n.N("config")
	checkWarning = i18n.N("warning")
	checkLocal   = i18n.N("local")
	checkResolve = i18n.N("resolve")
	checkConnect = i18n.N("connect")
	checkRemote  = i18n.N("remote")
	checkRecv    = i18n.N("recv (list)")
	checkSend    = i18n.N("send (write)")

	statusOK     = i18n.N("OK")
	statusNG     = i18n.N("NG")
	statusFailed = i18n.N("FAILED")
)

// reportFunc prints one line of goft test: which check, what it looked at,
// and how it turned out, each already in the reader's language.
type reportFunc func(check, detail, status string)

// status joins a result to what it found, as "OK  readable and writable".
func status(result, detail string) string {
	if detail == "" {
		return tr.Text(result)
	}
	return tr.Text(result) + "  " + detail
}

func runTest(ctx context.Context, out io.Writer) error {
	var w console.Table
	flush := func() { _, _ = w.WriteTo(out) }
	defer flush()

	report := func(check, detail, status string) {
		w.Row(tr.Text(check), detail, status)
	}

	cfgPath, err := findConfig()
	if err != nil {
		return startupError(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return startupError(err)
	}
	if err := config.Validate(cfg); err != nil {
		report(checkConfig, cfgPath, status(statusFailed, ""))
		flush()
		return startupError(i18n.Errorf("invalid configuration:\n%w", err))
	}
	report(checkConfig, cfgPath, status(statusOK, ""))

	// post_action: move is only valid for send; say so rather than letting it
	// surprise whoever later runs recv with this file.
	if err := config.ValidateForDirection(cfg, config.DirRecv); err != nil {
		report(checkConfig, "recv", status(statusNG, tr.Error(err)))
	}

	for _, warning := range config.Warnings(cfg) {
		report(checkWarning, warning.Localize(tr), "")
	}

	checkLocalDir(cfg.Local.Path, report)

	resolved, err := fsys.Resolve(cfg.Remote)
	if err != nil {
		report(checkResolve, string(cfg.Remote.Protocol), status(statusFailed, ""))
		flush()
		return startupError(err)
	}
	for _, t := range resolved.Trace {
		report(checkResolve, t.Field+" = "+t.Display(tr), "("+t.Source+")")
	}
	for _, warning := range resolved.Warnings {
		report(checkWarning, warning.Localize(tr), "")
	}

	// Nothing is dialled for a directory on this machine, so calling the check
	// "connect" would describe something that did not happen.
	opened := checkConnect
	if cfg.Remote.IsLocal() {
		opened = checkRemote
	}

	remote, err := fsys.NewRemote(ctx, cfg.Remote)
	if err != nil {
		report(opened, string(cfg.Remote.Protocol), status(statusFailed, ""))
		flush()
		return startupError(err)
	}
	defer remote.Close()
	report(opened, remote.Describe(), status(statusOK, ""))

	canRecv := checkList(ctx, remote, report)
	canSend := checkSendDir(ctx, cfg.Remote, remote, canRecv, report)

	if !canRecv && !canSend {
		flush()
		return startupError(i18n.Errorf("the remote can be reached but is usable in neither direction"))
	}
	return nil
}

// checkSendDir reports whether a send job could write to the remote.
//
// A destination that does not exist yet is the normal state before the first
// transfer, so this is not treated as a failure: the parent directory is
// probed instead, which is where the destination will be created.
func checkSendDir(ctx context.Context, cfg config.Remote, remote fsys.FS, exists bool, report reportFunc) bool {
	if exists {
		return probeWrite(ctx, remote, tr.Text(i18n.N("writable")), report)
	}

	parent := cfg
	parent.Path = parentDir(cfg)
	if parent.Path == cfg.Path {
		report(checkSend, tr.Text(i18n.N("remote path does not exist and has no parent to check")), status(statusNG, ""))
		return false
	}

	pfs, err := fsys.NewRemote(ctx, parent)
	if err != nil {
		report(checkSend, tr.Sprintf("cannot reach %s: %v", parent.Path, err), status(statusNG, ""))
		return false
	}
	defer pfs.Close()

	return probeWrite(ctx, pfs, tr.Sprintf("%s is writable; %s will be created on first transfer", parent.Path, cfg.Path), report)
}

// checkLocalDir reports on the local directory, which a send job reads from and a
// recv job writes to.
//
// Writability is probed rather than inferred: a directory that exists and is
// listable can still refuse a file, and finding that out during the first
// transfer is finding out too late.
func checkLocalDir(dir string, report reportFunc) {
	fi, err := os.Stat(dir)
	switch {
	case err != nil:
		report(checkLocal, dir, status(statusFailed, tr.Error(err)))
		return
	case !fi.IsDir():
		report(checkLocal, dir, status(statusFailed, tr.Text(i18n.N("not a directory"))))
		return
	}

	probe := filepath.Join(dir, fmt.Sprintf(".goft-test-%d", os.Getpid()))
	if err := os.Mkdir(probe, 0o755); err != nil {
		// Reading still works, so a send job is fine; only recv needs to write.
		report(checkLocal, dir, status(statusOK, tr.Text(i18n.N("readable; not writable, which recv would need"))))
		return
	}
	if err := os.Remove(probe); err != nil {
		report(checkLocal, dir, status(statusOK, tr.Sprintf("probe directory left behind: %s", probe)))
		return
	}
	report(checkLocal, dir, status(statusOK, tr.Text(i18n.N("readable and writable"))))
}

// checkList reports whether the remote directory can be read, which is what a
// recv job needs.
func checkList(ctx context.Context, remote fsys.FS, report reportFunc) bool {
	entries, err := remote.List(ctx, "")
	switch {
	case err == nil:
		report(checkRecv, tr.Sprintf("%d entries", len(entries)), status(statusOK, ""))
		return true
	case errors.Is(err, fs.ErrNotExist):
		report(checkRecv, tr.Text(i18n.N("remote path does not exist yet")), status(statusNG, ""))
		return false
	default:
		report(checkRecv, tr.Error(err), status(statusNG, ""))
		return false
	}
}

// parentDir returns the directory the remote path sits in, in whatever form
// that protocol writes paths: slash separated everywhere except a directory on
// this machine, which uses the host separator.
func parentDir(cfg config.Remote) string {
	if cfg.IsLocal() {
		return filepath.Dir(cfg.Path)
	}
	return path.Dir(cfg.Path)
}

// probeWrite creates and removes one directory to prove write access.
//
// The destination directory itself is never created here: a connectivity check
// that leaves an unexpected directory behind is worse than an unanswered
// question.
func probeWrite(ctx context.Context, target fsys.FS, detail string, report reportFunc) bool {
	probe := fmt.Sprintf(".goft-test-%d", os.Getpid())
	if err := target.MkdirAll(ctx, probe); err != nil {
		report(checkSend, tr.Error(err), status(statusNG, ""))
		return false
	}
	if err := target.Remove(ctx, probe); err != nil {
		report(checkSend, tr.Sprintf("probe directory left behind: %s", probe), status(statusOK, ""))
		return true
	}
	report(checkSend, detail, status(statusOK, ""))
	return true
}
