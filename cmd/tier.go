package cmd

import (
	"fmt"

	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/ui"
	"github.com/spf13/cobra"
)

var tierCmd = &cobra.Command{
	Use:   "tier",
	Short: "View or set your subscription tier",
	Long: `View the current tier and available tools, or set a new tier.

  ag3nts tier              Show current tier
  ag3nts tier set premium  Set tier to premium

Tiers:
  free     Gemini CLI only
  paid     Gemini CLI + Codex CLI
  premium  Gemini CLI + Codex CLI + Claude Code`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Current tier: %s\n\n", ui.Bold(string(cfg.General.Tier)))
		fmt.Println("Available tools:")
		for _, t := range cfg.AllowedTools() {
			ui.OK(t)
		}

		if cfg.General.Tier != config.TierPremium {
			fmt.Printf("\nUpgrade: ag3nts tier set premium\n")
		}
		return nil
	},
}

var tierSetCmd = &cobra.Command{
	Use:   "set <level>",
	Short: "Set your subscription tier",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tier := config.Tier(args[0])
		switch tier {
		case config.TierFree, config.TierPaid, config.TierPremium:
			// valid
		default:
			return fmt.Errorf("invalid tier %q (must be: free, paid, premium)", args[0])
		}

		cfg.General.Tier = tier
		if err := cfg.Save(layout.ConfigFile()); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		ui.OK(fmt.Sprintf("Tier set to %s", tier))
		fmt.Println("\nAvailable tools:")
		for _, t := range cfg.AllowedTools() {
			ui.OK(t)
		}

		fmt.Println("\nRun 'ag3nts install' to install any newly available tools.")
		return nil
	},
}

func init() {
	tierCmd.AddCommand(tierSetCmd)
	rootCmd.AddCommand(tierCmd)
}
