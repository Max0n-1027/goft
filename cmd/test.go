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
	"text/tabwriter"

	"github.com/spf13/cobra"

	"goft/internal/config"
	"goft/internal/fsys"
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Check the configuration and the connection without transferring anything",
	Long: "Check the configuration and the connection without transferring anything.\n\n" +
		"Both directions are reported, because the configuration file does not say\n" +
		"which one a job will be used for. A read-only account fails the send check\n" +
		"and still exits 0; only a remote that supports neither direction exits 2.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runTest(cmd.Context(), cmd.OutOrStdout())
	},
}

func init() { rootCmd.AddCommand(testCmd) }

func runTest(ctx context.Context, out io.Writer) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer w.Flush()

	report := func(check, detail, status string) {
		fmt.Fprintf(w, "%s\t%s\t%s\n", check, detail, status)
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
		report("config", cfgPath, "FAILED")
		w.Flush()
		return startupError(fmt.Errorf("invalid configuration:\n%w", err))
	}
	report("config", cfgPath, "OK")

	// post_action: move is only valid for send; say so rather than letting it
	// surprise whoever later runs recv with this file.
	if err := config.ValidateForDirection(cfg, config.DirRecv); err != nil {
		report("config", "recv", "NG  "+err.Error())
	}

	for _, warning := range config.Warnings(cfg) {
		report("warning", warning, "")
	}

	checkLocal(cfg.Local.Path, report)

	resolved, err := fsys.Resolve(cfg.Remote)
	if err != nil {
		report("resolve", string(cfg.Remote.Protocol), "FAILED")
		w.Flush()
		return startupError(err)
	}
	for _, t := range resolved.Trace {
		report("resolve", fmt.Sprintf("%s = %s", t.Field, t.Value), "("+t.Source+")")
	}
	for _, warning := range resolved.Warnings {
		report("warning", warning, "")
	}

	// Nothing is dialled for a directory on this machine, so calling the check
	// "connect" would describe something that did not happen.
	opened := "connect"
	if cfg.Remote.IsLocal() {
		opened = "remote"
	}

	remote, err := fsys.NewRemote(ctx, cfg.Remote)
	if err != nil {
		report(opened, string(cfg.Remote.Protocol), "FAILED")
		w.Flush()
		return startupError(err)
	}
	defer remote.Close()
	report(opened, remote.Describe(), "OK")

	canRecv := checkList(ctx, remote, report)
	canSend := checkSend(ctx, cfg.Remote, remote, canRecv, report)

	if !canRecv && !canSend {
		w.Flush()
		return startupError(errors.New("the remote can be reached but is usable in neither direction"))
	}
	return nil
}

// checkSend reports whether a send job could write to the remote.
//
// A destination that does not exist yet is the normal state before the first
// transfer, so this is not treated as a failure: the parent directory is
// probed instead, which is where the destination will be created.
func checkSend(ctx context.Context, cfg config.Remote, remote fsys.FS, exists bool, report func(string, string, string)) bool {
	if exists {
		return probeWrite(ctx, remote, "writable", report)
	}

	parent := cfg
	parent.Path = parentDir(cfg)
	if parent.Path == cfg.Path {
		report("send (write)", "remote path does not exist and has no parent to check", "NG")
		return false
	}

	pfs, err := fsys.NewRemote(ctx, parent)
	if err != nil {
		report("send (write)", "cannot reach "+parent.Path+": "+err.Error(), "NG")
		return false
	}
	defer pfs.Close()

	return probeWrite(ctx, pfs, parent.Path+" is writable; "+cfg.Path+" will be created on first transfer", report)
}

// checkLocal reports on the local directory, which a send job reads from and a
// recv job writes to.
//
// Writability is probed rather than inferred: a directory that exists and is
// listable can still refuse a file, and finding that out during the first
// transfer is finding out too late.
func checkLocal(dir string, report func(string, string, string)) {
	fi, err := os.Stat(dir)
	switch {
	case err != nil:
		report("local", dir, "FAILED  "+err.Error())
		return
	case !fi.IsDir():
		report("local", dir, "FAILED  not a directory")
		return
	}

	probe := filepath.Join(dir, fmt.Sprintf(".goft-test-%d", os.Getpid()))
	if err := os.Mkdir(probe, 0o755); err != nil {
		// Reading still works, so a send job is fine; only recv needs to write.
		report("local", dir, "OK  readable; not writable, which recv would need")
		return
	}
	if err := os.Remove(probe); err != nil {
		report("local", dir, "OK  probe directory left behind: "+probe)
		return
	}
	report("local", dir, "OK  readable and writable")
}

// checkList reports whether the remote directory can be read, which is what a
// recv job needs.
func checkList(ctx context.Context, remote fsys.FS, report func(string, string, string)) bool {
	entries, err := remote.List(ctx, "")
	switch {
	case err == nil:
		report("recv (list)", fmt.Sprintf("%d entries", len(entries)), "OK")
		return true
	case errors.Is(err, fs.ErrNotExist):
		report("recv (list)", "remote path does not exist yet", "NG")
		return false
	default:
		report("recv (list)", err.Error(), "NG")
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
func probeWrite(ctx context.Context, target fsys.FS, detail string, report func(string, string, string)) bool {
	probe := fmt.Sprintf(".goft-test-%d", os.Getpid())
	if err := target.MkdirAll(ctx, probe); err != nil {
		report("send (write)", err.Error(), "NG")
		return false
	}
	if err := target.Remove(ctx, probe); err != nil {
		report("send (write)", "probe directory left behind: "+probe, "OK")
		return true
	}
	report("send (write)", detail, "OK")
	return true
}
