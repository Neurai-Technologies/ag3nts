package cmd

import (
	"fmt"

	"github.com/rohanrgit/ag3nts/internal/ui"
	"github.com/rohanrgit/ag3nts/internal/workflow"
	"github.com/spf13/cobra"
)

var workflowCmd = &cobra.Command{
	Use:   "workflow",
	Short: "Manage shareable config repos (workflows)",
	Long: `Workflows are git repos containing shared agent configurations, pipeline
instructions, and tool settings. Install a workflow to link its configs
into your ag3nts setup.

  ag3nts workflow install <name> --repo <url>
  ag3nts workflow update [<name>]
  ag3nts workflow list
  ag3nts workflow switch <name>`,
}

var workflowInstallCmd = &cobra.Command{
	Use:   "install <name>",
	Short: "Clone and link a workflow repo",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		repo, _ := cmd.Flags().GetString("repo")
		branch, _ := cmd.Flags().GetString("branch")

		if repo == "" {
			return fmt.Errorf("--repo flag is required")
		}

		ui.Header("Installing workflow")
		if err := workflow.Install(name, repo, branch, layout, cfg); err != nil {
			return err
		}

		// Count assets
		sharedDir := workflow.ActiveWorkflowSharedDir(layout, cfg)
		agents, stages := workflow.CountAssets(sharedDir)
		fmt.Fprintf(cmd.ErrOrStderr(), "\n  %d agents, %d pipeline stages linked\n", agents, stages)
		fmt.Fprintln(cmd.ErrOrStderr(), "\nRun 'ag3nts' to launch the master agent.")
		return nil
	},
}

var workflowUpdateCmd = &cobra.Command{
	Use:   "update [<name>]",
	Short: "Pull latest changes for a workflow",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := cfg.Workflows.Active
		if len(args) > 0 {
			name = args[0]
		}
		if name == "" {
			return fmt.Errorf("no active workflow. Run: ag3nts workflow install <name> --repo <url>")
		}
		return workflow.Update(name, layout)
	},
}

var workflowListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show installed workflows",
	RunE: func(cmd *cobra.Command, args []string) error {
		names, err := workflow.List(layout)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			fmt.Println("No workflows installed.")
			fmt.Println("Run: ag3nts workflow install <name> --repo <url>")
			return nil
		}
		for _, name := range names {
			marker := "  "
			if name == cfg.Workflows.Active {
				marker = "* "
			}
			fmt.Printf("%s%s\n", marker, name)
		}
		return nil
	},
}

var workflowSwitchCmd = &cobra.Command{
	Use:   "switch <name>",
	Short: "Change the active workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		// Verify workflow exists
		workflowDir := layout.WorkflowDir(name)
		sharedDir := workflowDir + "/shared"
		if _, err := workflow.List(layout); err != nil {
			return err
		}

		// Re-link configs
		ui.Step(fmt.Sprintf("Switching to workflow %q...", name))
		if err := workflow.Install(name, "", "", layout, cfg); err != nil {
			// If it's already cloned, just re-link
			_ = sharedDir
		}

		cfg.Workflows.Active = name
		return cfg.Save(layout.ConfigFile())
	},
}

func init() {
	workflowInstallCmd.Flags().String("repo", "", "Git repository URL")
	workflowInstallCmd.Flags().String("branch", "main", "Git branch")
	workflowCmd.AddCommand(workflowInstallCmd)
	workflowCmd.AddCommand(workflowUpdateCmd)
	workflowCmd.AddCommand(workflowListCmd)
	workflowCmd.AddCommand(workflowSwitchCmd)
	rootCmd.AddCommand(workflowCmd)
}
