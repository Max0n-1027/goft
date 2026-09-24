package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"goft/internal/config"
)

var flagDryRun bool

func addDryRun(c *cobra.Command) *cobra.Command {
	c.Flags().BoolVar(&flagDryRun, "dry-run", false, "list what would be transferred without transferring anything")
	return c
}

var sendCmd = addDryRun(&cobra.Command{
	Use:   "send",
	Short: "Transfer from the local directory to the other side once",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, stop := signalContext(cmd.Context())
		defer stop()
		return runOnce(ctx, config.DirSend, flagDryRun)
	},
})

var recvCmd = addDryRun(&cobra.Command{
	Use:   "recv",
	Short: "Transfer from the other side to the local directory once",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, stop := signalContext(cmd.Context())
		defer stop()
		return runOnce(ctx, config.DirRecv, flagDryRun)
	},
})

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Watch a directory and transfer continuously",
	Long: "Watch a directory and transfer continuously.\n\n" +
		"Choose a direction with `goft serve send` or `goft serve recv`.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

var serveSendCmd = addDryRun(&cobra.Command{
	Use:   "send",
	Short: "Watch the local directory and transfer to the other side",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, stop := signalContext(cmd.Context())
		defer stop()
		return runServe(ctx, config.DirSend, flagDryRun)
	},
})

var serveRecvCmd = addDryRun(&cobra.Command{
	Use:   "recv",
	Short: "Watch the other side and transfer to the local directory",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, stop := signalContext(cmd.Context())
		defer stop()
		return runServe(ctx, config.DirRecv, flagDryRun)
	},
})

// signalContext cancels on Ctrl+C and on SIGTERM.
//
// Cancelling stops the run from starting anything new; a file already under
// way is finished, so that a stop never leaves more than a temporary file
// behind. One that has stalled is dropped after remote.io_timeout, which bounds
// how long a stop can take.
//
// syscall.SIGTERM is defined on Windows as well, so this compiles everywhere
// even though only os.Interrupt is ever delivered there.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

func init() {
	serveCmd.AddCommand(serveSendCmd, serveRecvCmd)
	rootCmd.AddCommand(sendCmd, recvCmd, serveCmd)
}
