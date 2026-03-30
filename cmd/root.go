package cmd

import (
	"fmt"
	"os"

	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/spf13/cobra"
)

// Version is set by goreleaser at build time.
var Version = "dev"

var (
	layout *paths.Layout
	cfg    *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "ag3nts",
	Short: "Master agent orchestrator for AI coding tools",
	Long: `ag3nts manages Claude Code, Gemini CLI, and Codex CLI as a unified
multi-agent system. It installs tools, manages shared configurations
via workflows, and launches Claude Code with an MCP server that exposes
Gemini and Codex as callable tools.

  ag3nts              Launch master agent (Claude Code + MCP server)
  ag3nts install      Install tools for your tier
  ag3nts run <tool>   Launch a specific tool directly
  ag3nts workflow     Manage shareable config repos
  ag3nts status       Show system health
  ag3nts doctor       Run diagnostics`,
	Version: Version,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip layout detection for help and version
		if cmd.Name() == "help" || cmd.Name() == "version" {
			return nil
		}

		var err error
		layout, err = paths.Detect()
		if err != nil {
			return fmt.Errorf("could not locate ag3nts project: %w\n\nMake sure you're in an ag3nts project directory or have your SSD mounted", err)
		}

		cfg, err = config.Load(layout.ConfigFile())
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// No subcommand = launch master agent
		return runMaster(cmd, args)
	},
}

// Execute is the entry point called from main.go.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// GetLayout returns the detected project layout (available after PersistentPreRunE).
func GetLayout() *paths.Layout {
	return layout
}

// GetConfig returns the loaded config (available after PersistentPreRunE).
func GetConfig() *config.Config {
	return cfg
}
