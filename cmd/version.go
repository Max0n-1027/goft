package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Build information, set with -ldflags at release time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the goft version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "goft %s (commit %s, built %s)\n", version, commit, date)
	},
}

func init() { rootCmd.AddCommand(versionCmd) }
