package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rohanrgit/ag3nts/internal/symlinks"
	"github.com/rohanrgit/ag3nts/internal/tools"
	"github.com/rohanrgit/ag3nts/internal/ui"
	"github.com/spf13/cobra"
)

const systemBinDir = "/usr/local/bin"

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install ag3nts and AI coding tools for your tier",
	Long: `Install the ag3nts binary to /usr/local/bin and install Claude Code,
Gemini CLI, and Codex CLI based on your subscription tier. Creates the
directory layout, installs tools, sets up symlinks, and makes ag3nts
runnable from anywhere.

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

		// Install ag3nts binary to /usr/local/bin
		if err := installBinary(); err != nil {
			ui.Fail(fmt.Sprintf("binary install: %v", err))
			fmt.Fprintln(cmd.ErrOrStderr(), "  You may need: sudo ag3nts install")
		} else {
			ui.OK(fmt.Sprintf("ag3nts binary → %s/ag3nts", systemBinDir))
		}

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
		fmt.Fprintln(cmd.ErrOrStderr(), "\nYou can now run 'ag3nts' from anywhere to launch the orchestrator.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
}

// installBinary copies the running ag3nts binary into {base}/bin/ and
// creates a symlink at /usr/local/bin/ag3nts pointing to it.
func installBinary() error {
	// Resolve the currently running binary.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("eval symlinks: %w", err)
	}

	// Copy binary into {base}/bin/ag3nts.
	destBin := filepath.Join(layout.Bin, "ag3nts")
	if self != destBin {
		data, err := os.ReadFile(self)
		if err != nil {
			return fmt.Errorf("read binary: %w", err)
		}
		if err := os.WriteFile(destBin, data, 0755); err != nil {
			return fmt.Errorf("write binary: %w", err)
		}
	}

	// Create /usr/local/bin directory if it doesn't exist.
	if err := os.MkdirAll(systemBinDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", systemBinDir, err)
	}

	// Create or update the symlink.
	linkPath := filepath.Join(systemBinDir, "ag3nts")

	// Check if symlink already points to the right place.
	if target, err := os.Readlink(linkPath); err == nil && target == destBin {
		return nil // already correct
	}

	// Remove existing file/symlink at the path.
	os.Remove(linkPath)

	return os.Symlink(destBin, linkPath)
}
