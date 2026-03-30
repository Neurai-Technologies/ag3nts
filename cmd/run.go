package cmd

import (
	"github.com/rohanrgit/ag3nts/internal/run"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run <tool>",
	Short: "Launch a specific tool directly",
	Long: `Launch Claude Code, Gemini CLI, or Codex CLI directly.
Checks tier permissions before launching. Pass additional arguments after --.

  ag3nts run claude
  ag3nts run gemini
  ag3nts run codex -- --model gpt-5.4`,
	Args:               cobra.MinimumNArgs(1),
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		toolName := args[0]
		var toolArgs []string
		if len(args) > 1 {
			toolArgs = args[1:]
		}
		return run.Tool(toolName, toolArgs, layout, cfg)
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
