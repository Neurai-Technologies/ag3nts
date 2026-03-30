package cmd

import (
	"context"

	"github.com/rohanrgit/ag3nts/internal/tools"
	"github.com/rohanrgit/ag3nts/internal/update"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update [tool]",
	Short: "Update installed tools to latest versions",
	Long: `Check for and install new versions of managed tools.
Current binaries are cached for rollback before updating.

  ag3nts update          Update all tools
  ag3nts update codex    Update only Codex CLI`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		if len(args) > 0 {
			tool := tools.Get(args[0])
			if tool == nil {
				return nil
			}
			return update.Apply(ctx, tool, layout)
		}

		return update.UpdateAll(ctx, layout, cfg)
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
