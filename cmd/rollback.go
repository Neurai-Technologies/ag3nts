package cmd

import (
	"fmt"

	"github.com/rohanrgit/ag3nts/internal/tools"
	"github.com/rohanrgit/ag3nts/internal/update"
	"github.com/spf13/cobra"
)

var rollbackCmd = &cobra.Command{
	Use:   "rollback <tool>",
	Short: "Restore previous version of a tool",
	Long: `Restore the previous version of a tool from the cached binary.
Only one version of rollback is kept per tool.

  ag3nts rollback codex
  ag3nts rollback gemini`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tool := tools.Get(args[0])
		if tool == nil {
			return fmt.Errorf("unknown tool: %s", args[0])
		}
		return update.Rollback(tool, layout)
	},
}

func init() {
	rootCmd.AddCommand(rollbackCmd)
}
