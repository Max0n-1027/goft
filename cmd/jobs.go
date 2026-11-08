package cmd

import (
	"io"

	"github.com/spf13/cobra"

	"goft/internal/config"
	"goft/internal/console"
	"goft/internal/i18n"
)

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: i18n.N("List the jobs in the configuration"),
	Long: i18n.N("List the jobs in the configuration: each one's name, direction, whether the\n" +
		"service runs it, how often it looks for files, and where it moves them from\n" +
		"and to. The configuration is checked first, as the service would check it."),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return startupError(err)
		}
		listJobs(cmd.OutOrStdout(), cfg)
		return nil
	},
}

func init() { rootCmd.AddCommand(jobsCmd) }

// listJobs prints one row per job, in the order the configuration gives them.
func listJobs(out io.Writer, cfg *config.Config) {
	var t console.Table
	t.Row(tr.Text(i18n.N("NAME")), tr.Text(i18n.N("DIRECTION")), tr.Text(i18n.N("STATE")),
		tr.Text(i18n.N("POLL")), tr.Text(i18n.N("FROM -> TO")))
	for _, j := range cfg.Jobs {
		state := tr.Text(i18n.N("enabled"))
		if !j.Enabled {
			state = tr.Text(i18n.N("disabled"))
		}
		from, to := j.Local.Path, j.Remote.Describe()
		if j.Direction == config.DirRecv {
			from, to = to, from
		}
		t.Row(j.Name, string(j.Direction), state, j.PollInterval.String(), from+" -> "+to)
	}
	_, _ = t.WriteTo(out)
}
