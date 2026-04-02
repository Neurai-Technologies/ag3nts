package cmd

import (
	"context"
	"fmt"

	"github.com/rohanrgit/ag3nts/internal/symlinks"
	"github.com/rohanrgit/ag3nts/internal/tools"
	"github.com/rohanrgit/ag3nts/internal/ui"
	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install AI coding tools for your tier",
	Long: `Install Claude Code, Gemini CLI, and Codex CLI based on your subscription tier.
Creates the directory layout, installs tools, sets up symlinks, and runs OAuth setup.

  Free tier:    Gemini CLI
  Paid tier:    Gemini CLI + Codex CLI
  Premium tier: Gemini CLI + Codex CLI + Claude Code`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		ui.Header("ag3nts install")
		fmt.Fprintf(cmd.ErrOrStderr(), "Tier: %s\n\n", cfg.General.Tier)

		// Ensure directory layout exists
		if err := layout.EnsureDirs(); err != nil {
			return fmt.Errorf("create directories: %w", err)
		}
		ui.OK("Directory layout created")

		// Set up home directory symlinks (~/.claude, ~/.codex, ~/.gemini)
		if err := symlinks.CreateAll(layout); err != nil {
			return fmt.Errorf("create symlinks: %w", err)
		}

		// Determine which tools to install based on tier
		allowed := cfg.AllowedTools()
		var failures []string

		for _, toolName := range allowed {
			tool := tools.Get(toolName)
			if tool == nil {
				continue
			}

			// Install Node.js first if we need Gemini
			if toolName == "gemini" {
				if err := tools.InstallNode(ctx, layout, cfg.Node.Version); err != nil {
					ui.Fail(fmt.Sprintf("Node.js: %v", err))
					failures = append(failures, "node")
					continue
				}
			}

			if err := tool.Install(ctx, layout); err != nil {
				ui.Fail(fmt.Sprintf("%s: %v", toolName, err))
				failures = append(failures, toolName)
				continue
			}
		}

		// Save default config if it doesn't exist yet
		if err := cfg.Save(layout.ConfigFile()); err != nil {
			ui.Fail(fmt.Sprintf("save config: %v", err))
		}

		// Summary
		fmt.Fprintln(cmd.ErrOrStderr())
		if len(failures) > 0 {
			ui.Header("Install completed with errors")
			for _, f := range failures {
				ui.Fail(f)
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "\nRun 'ag3nts doctor' to diagnose issues.")
			return fmt.Errorf("%d tool(s) failed to install", len(failures))
		}

		ui.Header("Install complete")
		fmt.Fprintf(cmd.ErrOrStderr(), "Installed %d tool(s) for %s tier.\n", len(allowed), cfg.General.Tier)
		fmt.Fprintln(cmd.ErrOrStderr(), "\nNext steps:")
		fmt.Fprintln(cmd.ErrOrStderr(), "  ag3nts workflow install <name> --repo <url>  # install your workflow")
		fmt.Fprintln(cmd.ErrOrStderr(), "  ag3nts                                       # launch master agent")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
}
