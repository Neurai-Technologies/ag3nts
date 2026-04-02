package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rohanrgit/ag3nts/internal/symlinks"
	"github.com/rohanrgit/ag3nts/internal/ui"
	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove ag3nts tools and binaries (preserves user data)",
	Long: `Removes installed tools, Node.js, cache, state, the ag3nts binary from
/usr/local/bin, and home directory symlinks. Preserves user data:

  Kept:    config/ (agent configs, auth, memories, workflows, ag3nts.toml)
  Removed: tools/, cache/, state/, bin/, /usr/local/bin/ag3nts symlink,
           ~/.claude, ~/.codex, ~/.gemini symlinks`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ui.Header("ag3nts uninstall")

		// 1. Remove home directory symlinks (~/.claude, ~/.codex, ~/.gemini).
		//    Only removes if they point to our config dirs — won't touch
		//    unrelated files at those paths.
		ui.Header("Removing home directory symlinks")
		for _, spec := range symlinks.AllSpecs(layout) {
			if removeOwnedSymlink(spec.Link, spec.Target) {
				ui.OK(fmt.Sprintf("Removed %s", spec.Link))
			} else {
				ui.Skip(fmt.Sprintf("%s (not an ag3nts symlink, left intact)", spec.Link))
			}
		}

		// 2. Remove installed tools, cache, state.
		dirsToRemove := []struct {
			path string
			name string
		}{
			{layout.Tools, "tools"},
			{layout.Cache, "cache"},
			{layout.State, "state"},
		}

		ui.Header("Removing ag3nts directories")
		for _, d := range dirsToRemove {
			if err := os.RemoveAll(d.path); err != nil {
				ui.Fail(fmt.Sprintf("%s: %v", d.name, err))
			} else {
				ui.OK(fmt.Sprintf("Removed %s/", d.name))
			}
		}

		// 3. Remove /usr/local/bin/ag3nts symlink.
		ui.Header("Removing ag3nts from PATH")
		linkPath := filepath.Join(systemBinDir, "ag3nts")
		binTarget := filepath.Join(layout.Bin, "ag3nts")
		if removeOwnedSymlink(linkPath, binTarget) {
			ui.OK(fmt.Sprintf("Removed %s", linkPath))
		} else {
			ui.Skip(fmt.Sprintf("%s (not an ag3nts symlink, left intact)", linkPath))
		}

		// 4. Remove bin/ directory.
		if err := os.RemoveAll(layout.Bin); err != nil {
			ui.Fail(fmt.Sprintf("bin: %v", err))
		} else {
			ui.OK("Removed bin/")
		}

		ui.Header("Uninstall complete")
		fmt.Fprintln(cmd.ErrOrStderr(), "User data preserved in config/ (agent configs, auth, memories, workflows).")
		fmt.Fprintln(cmd.ErrOrStderr(), "To fully remove everything: rm -rf", layout.Base)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}

// removeOwnedSymlink removes a symlink only if it points to the expected target.
// Returns true if the symlink was removed, false if it was skipped.
func removeOwnedSymlink(link, expectedTarget string) bool {
	info, err := os.Lstat(link)
	if err != nil {
		return false // doesn't exist
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false // not a symlink
	}
	target, err := os.Readlink(link)
	if err != nil || target != expectedTarget {
		return false // points elsewhere
	}
	os.Remove(link)
	return true
}
