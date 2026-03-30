package cmd

import (
	"github.com/rohanrgit/ag3nts/internal/run"
	"github.com/spf13/cobra"
)

// runMaster is called when ag3nts is invoked with no subcommand.
func runMaster(cmd *cobra.Command, args []string) error {
	return run.Master(layout, cfg)
}
