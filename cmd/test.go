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
	"strings"

	"github.com/spf13/cobra"

	"goft/internal/config"
	"goft/internal/console"
	"goft/internal/fsys"
	"goft/internal/i18n"
)

var testCmd = &cobra.Command{
	Use:   "test [job...]",
	Short: i18n.N("Check the configuration and each job's connection without transferring anything"),
	Long: i18n.N("Check the configuration and each job's connection without transferring anything.\n\n" +
		"Each job is checked the way it uses each side: the side it takes files from\n" +
		"must be readable, the side it puts them on writable, and a side it deletes\n" +
		"from writable as well. With no job named, every enabled job is checked. A\n" +
		"job that cannot be used as configured makes goft test exit 2."),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTest(cmd.Context(), cmd.OutOrStdout(), args)
	},
}

func init() { rootCmd.AddCommand(testCmd) }

// The checks goft test reports, and how each turned out. They are marked
// here and translated as they are printed.
var (
	checkConfig  = i18n.N("config")
	checkJob     = i18n.N("job")
	checkWarning = i18n.N("warning")
	checkLocal   = i18n.N("local")
	checkResolve = i18n.N("resolve")
	checkConnect = i18n.N("connect")
	checkRemote  = i18n.N("remote")
	checkRecv    = i18n.N("recv (list)")
	checkSend    = i18n.N("send (write)")
	checkDelete  = i18n.N("recv (delete)")

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

func runTest(ctx context.Context, out io.Writer, names []string) error {
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
	cfg, err := loadConfig()
	if err != nil {
		report(checkConfig, cfgPath, status(statusFailed, ""))
		flush()
		return startupError(err)
	}
	report(checkConfig, cfg.SourceFile, status(statusOK, ""))
	for _, f := range cfg.JobFiles {
		report(checkConfig, f, status(statusOK, ""))
	}

	jobs, err := pick(cfg, names)
	if err != nil {
		flush()
		return startupError(err)
	}
	var unusable []string
	for _, j := range jobs {
		w.Row("")
		report(checkJob, j.Name, string(j.Direction))
		if !testJob(ctx, j, report) {
			unusable = append(unusable, j.Name)
		}
	}
	if len(unusable) > 0 {
		flush()
		return startupError(i18n.Errorf("jobs that cannot be used as configured: %s", strings.Join(unusable, ", ")))
	}
	return nil
}

// testJob checks one job the way it uses each side, and reports whether it can
// run as configured.
func testJob(ctx context.Context, j *config.Job, report reportFunc) bool {
	for _, warning := range config.Warnings(j) {
		report(checkWarning, warning.Localize(tr), "")
	}

	// The local side is written to by recv, and by send when the job takes its
	// sources away or tidies up the directories it emptied.
	takesAway := j.PostAction == config.PostDelete || j.PostAction == config.PostMove || j.RemoveEmptyDirs
	ok := checkLocalDir(j.Local.Path, j.Direction == config.DirRecv || takesAway, report)

	resolved, err := fsys.Resolve(j.Remote)
	if err != nil {
		report(checkResolve, string(j.Remote.Protocol), status(statusFailed, tr.Error(err)))
		return false
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
	if j.Remote.IsLocal() {
		opened = checkRemote
	}
	remote, err := fsys.NewRemote(ctx, j.Remote)
	if err != nil {
		report(opened, string(j.Remote.Protocol), status(statusFailed, tr.Error(err)))
		return false
	}
	defer remote.Close()
	report(opened, remote.Describe(), status(statusOK, ""))

	if j.Direction == config.DirSend {
		return checkSendDir(ctx, j.Remote, remote, report) && ok
	}
	listed := checkList(ctx, remote, report)
	if listed && takesAway {
		// Deleting is not something to try on the server's real files, but a
		// directory that cannot be written to will not let one go either.
		listed = probeWrite(ctx, remote, checkDelete, tr.Text(i18n.N("writable")), report)
	}
	return listed && ok
}

// checkSendDir reports whether a send job could write to the remote.
//
// A destination that does not exist yet is the normal state before the first
// transfer, so this is not treated as a failure: the parent directory is
// probed instead, which is where the destination will be created.
func checkSendDir(ctx context.Context, cfg config.Remote, remote fsys.FS, report reportFunc) bool {
	_, err := remote.List(ctx, "")
	switch {
	case err == nil:
		return probeWrite(ctx, remote, checkSend, tr.Text(i18n.N("writable")), report)
	case !errors.Is(err, fs.ErrNotExist):
		report(checkSend, tr.Error(err), status(statusNG, ""))
		return false
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

	return probeWrite(ctx, pfs, checkSend, tr.Sprintf("%s is writable; %s will be created on first transfer", parent.Path, cfg.Path), report)
}

// checkLocalDir reports on the local directory, which a send job reads from and a
// recv job writes to, and reports whether it can be used as the job needs.
//
// Writability is probed rather than inferred: a directory that exists and is
// listable can still refuse a file, and finding that out during the first
// transfer is finding out too late.
func checkLocalDir(dir string, needWrite bool, report reportFunc) bool {
	if _, err := os.ReadDir(dir); err != nil {
		report(checkLocal, dir, status(statusFailed, tr.Error(err)))
		return false
	}

	probe := filepath.Join(dir, fmt.Sprintf(".goft-test-%d", os.Getpid()))
	if err := os.Mkdir(probe, 0o755); err != nil {
		if needWrite {
			report(checkLocal, dir, status(statusNG, tr.Text(i18n.N("readable; not writable, which this job needs"))))
			return false
		}
		report(checkLocal, dir, status(statusOK, tr.Text(i18n.N("readable; not writable, which this job does not need"))))
		return true
	}
	if err := os.Remove(probe); err != nil {
		report(checkLocal, dir, status(statusOK, tr.Sprintf("probe directory left behind: %s", probe)))
		return true
	}
	report(checkLocal, dir, status(statusOK, tr.Text(i18n.N("readable and writable"))))
	return true
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
func probeWrite(ctx context.Context, target fsys.FS, check, detail string, report reportFunc) bool {
	probe := fmt.Sprintf(".goft-test-%d", os.Getpid())
	if err := target.MkdirAll(ctx, probe); err != nil {
		report(check, tr.Error(err), status(statusNG, ""))
		return false
	}
	if err := target.Remove(ctx, probe); err != nil {
		report(check, tr.Sprintf("probe directory left behind: %s", probe), status(statusOK, ""))
		return true
	}
	report(check, detail, status(statusOK, ""))
	return true
}
