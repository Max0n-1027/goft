package cmd

import (
	"io"

	"github.com/spf13/cobra"

	"goft/internal/i18n"
	"goft/schema"
)

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: i18n.N("Print the JSON Schema of the configuration file"),
	Long: i18n.N("Print the JSON Schema (draft-07) of the configuration file: the schema goft\n" +
		"checks every file against when it starts. An editor that understands JSON\n" +
		"Schema can check the configuration with it as it is written, and this one\n" +
		"always matches the goft that printed it.\n\n" +
		"  goft schema > goft.schema.json"),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := io.WriteString(cmd.OutOrStdout(), schema.JSON)
		return err
	},
}

func init() { rootCmd.AddCommand(schemaCmd) }
