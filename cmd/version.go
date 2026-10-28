package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"goft/internal/i18n"
)

// Build information, set with -ldflags at release time.
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
		// The same in every language: the release workflow, and anything else
		// that checks which build it is running, reads this line.
		fmt.Fprintf(cmd.OutOrStdout(), "goft %s (commit %s, built %s)\n", version, commit, date)
	},
}

func init() { rootCmd.AddCommand(versionCmd) }
