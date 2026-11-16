package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/Max0n-1027/goft/internal/i18n"
)

// Build information, set with -ldflags by build.sh. A goft built any other way
// — go install, or a plain go build — reports what [stamped] can recover from
// the build information the Go toolchain records in every binary instead.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: i18n.N("Print the goft version"),
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		info, _ := debug.ReadBuildInfo()
		v, c := stamped(version, commit, info)
		// The same in every language: the release workflow, and anything else
		// that checks which build it is running, reads this line.
		fmt.Fprintf(cmd.OutOrStdout(), "goft %s (commit %s, built %s)\n", v, c, date)
	},
}

// shortRevision is how much of a commit hash goft reports, as git abbreviates
// one by default.
const shortRevision = 7

// stamped returns the version and commit goft reports.
//
// What -ldflags set always wins. Where it set nothing, info fills in what the
// Go toolchain recorded: the module version, which is the release go install
// fetched or one derived from the git checkout go build ran in, and the
// revision of that checkout. The build date is not recorded, so it stays as
// set. info may be nil, when the binary carries no build information.
func stamped(version, commit string, info *debug.BuildInfo) (string, string) {
	if info == nil {
		return version, commit
	}
	if v := info.Main.Version; version == "dev" && v != "" && v != "(devel)" {
		version = v
	}
	if commit == "none" {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				commit = s.Value[:min(len(s.Value), shortRevision)]
			}
		}
	}
	return version, commit
}

func init() { rootCmd.AddCommand(versionCmd) }
