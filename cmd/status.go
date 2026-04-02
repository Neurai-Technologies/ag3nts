package cmd

import (
	"fmt"

	"github.com/rohanrgit/ag3nts/internal/tools"
	"github.com/rohanrgit/ag3nts/internal/ui"
	"github.com/rohanrgit/ag3nts/internal/workflow"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show system health at a glance",
	Long: `Display installed tools, versions, authentication status,
active workflow, tier, and MCP server configuration.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ui.Header(fmt.Sprintf("ag3nts %s", Version))

		// Tier
		fmt.Printf("Tier: %s\n\n", ui.Bold(string(cfg.General.Tier)))

		// Tools
		fmt.Println("Tools:")
		for _, toolName := range []string{"gemini", "codex", "claude"} {
			tool := tools.Get(toolName)
			if tool == nil {
				continue
			}

			allowed := cfg.ToolAllowed(toolName)
			installed := tool.IsInstalled(layout)

			if !allowed {
				ui.Skip(fmt.Sprintf("%s (requires %s tier)", toolName, tool.RequiredTier()))
				continue
			}

			if installed {
				ver, _ := tool.InstalledVersion(layout)
				authed, _ := tool.AuthStatus(layout)
				authStr := "not authenticated"
				if authed {
					authStr = "authenticated"
				}
				ui.OK(fmt.Sprintf("%s %s (%s)", toolName, ver, authStr))
			} else {
				ui.Fail(fmt.Sprintf("%s — not installed", toolName))
			}
		}

		// Workflow
		fmt.Println()
		if cfg.Workflows.Active != "" {
			sharedDir := workflow.ActiveWorkflowSharedDir(layout, cfg)
			agents, stages := workflow.CountAssets(sharedDir)
			ui.OK(fmt.Sprintf("Workflow: %s (%d agents, %d pipeline stages)", cfg.Workflows.Active, agents, stages))
		} else {
			ui.Fail("No workflow installed")
		}

		// Orchestrator
		fmt.Println()
		fmt.Println("Orchestrator:")
		if cfg.Orchestrator.Primary != "" {
			ui.OK(fmt.Sprintf("Primary: %s", cfg.Orchestrator.Primary))
		} else {
			ui.Skip("Primary: auto-detect (not configured)")
		}
		ui.OK(fmt.Sprintf("Max concurrency: %d", cfg.Orchestrator.MaxConcurrency))
		ui.OK(fmt.Sprintf("Routing rules: %d", len(cfg.Routing.Rules)))
		httpCount := 0
		for _, a := range cfg.Agents {
			if a.Type == "http" {
				httpCount++
			}
		}
		if httpCount > 0 {
			ui.OK(fmt.Sprintf("HTTP agents: %d", httpCount))
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
