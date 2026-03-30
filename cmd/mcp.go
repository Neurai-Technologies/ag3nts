package cmd

import (
	"github.com/rohanrgit/ag3nts/internal/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP server commands",
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the MCP stdio server (called by Claude Code)",
	Long: `Starts the ag3nts MCP server on stdin/stdout using the JSON-RPC protocol.
This is not meant to be called directly — Claude Code spawns it via
the mcpServers configuration in settings.json.

Exposes two tools:
  gemini_query  Send a prompt to Gemini CLI
  codex_query   Send a prompt to Codex CLI`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return mcp.Serve(layout)
	},
}

func init() {
	mcpCmd.AddCommand(mcpServeCmd)
	rootCmd.AddCommand(mcpCmd)
}
