package cmd

import (
	"github.com/rohanrgit/ag3nts/internal/doctor"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostic checks",
	Long: `Check the health of your ag3nts installation:
  - Tools installed and correct versions
  - Symlinks valid
  - Authentication configured
  - Workflow linked
  - MCP server configured
  - Node.js functional (for Gemini)
  - Disk space`,
	RunE: func(cmd *cobra.Command, args []string) error {
		results := doctor.RunAll(layout, cfg)
		doctor.Print(results)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
