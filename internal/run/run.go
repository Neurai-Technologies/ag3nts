package run

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/tools"
	"github.com/rohanrgit/ag3nts/internal/ui"
	"github.com/rohanrgit/ag3nts/internal/workflow"
)

// Tool launches a specific tool with tier gating.
func Tool(toolName string, args []string, layout *paths.Layout, cfg *config.Config) error {
	// Tier check
	if !cfg.ToolAllowed(toolName) {
		tierNeeded := "paid"
		if toolName == "claude" {
			tierNeeded = "premium"
		}
		return fmt.Errorf("%s requires %s tier. Run: ag3nts tier set %s", toolName, tierNeeded, tierNeeded)
	}

	tool := tools.Get(toolName)
	if tool == nil {
		return fmt.Errorf("unknown tool: %s (available: claude, codex, gemini)", toolName)
	}

	if !tool.IsInstalled(layout) {
		return fmt.Errorf("%s is not installed. Run: ag3nts install", toolName)
	}

	// Codex: concatenate shared config before launch
	if toolName == "codex" {
		if err := concatCodexConfig(layout, cfg); err != nil {
			ui.Fail(fmt.Sprintf("codex config concat: %v (launching anyway)", err))
		}
	}

	// Resolve binary and exec
	binary := tool.BinaryPath(layout)
	return execTool(binary, toolName, args)
}

// Master launches Claude Code as the master agent with the MCP server configured.
func Master(layout *paths.Layout, cfg *config.Config) error {
	// For master mode, we need Claude Code
	if cfg.General.Tier == config.TierPremium {
		claude := tools.Get("claude")
		if claude == nil || !claude.IsInstalled(layout) {
			return fmt.Errorf("Claude Code not installed. Run: ag3nts install")
		}
		binary := claude.BinaryPath(layout)
		ui.Step("Launching master agent (Claude Code + MCP server)...")
		return execTool(binary, "claude", nil)
	}

	// Non-premium: fall back to best available tool
	if cfg.General.Tier == config.TierPaid {
		ui.Step("Premium tier required for master agent. Launching Codex CLI instead...")
		return Tool("codex", nil, layout, cfg)
	}

	ui.Step("Premium tier required for master agent. Launching Gemini CLI instead...")
	return Tool("gemini", nil, layout, cfg)
}

// execTool replaces the current process with the tool binary.
func execTool(binary string, name string, args []string) error {
	execArgs := []string{name}
	execArgs = append(execArgs, args...)

	env := os.Environ()

	return syscall.Exec(binary, execArgs, env)
}

// concatCodexConfig concatenates shared ag3nts.md with codex AGENTS.md
func concatCodexConfig(layout *paths.Layout, cfg *config.Config) error {
	sharedDir := workflow.ActiveWorkflowSharedDir(layout, cfg)
	if sharedDir == "" {
		return nil // no workflow, nothing to concat
	}

	agentsPath := filepath.Join(sharedDir, "ag3nts.md")
	codexAgentsPath := filepath.Join(sharedDir, "codex-cli", "AGENTS.md")
	destPath := filepath.Join(layout.ConfigDir("codex"), "AGENTS.md")

	// Read shared instructions
	shared, err := os.ReadFile(agentsPath)
	if err != nil {
		return nil // no shared file, skip
	}

	// Read codex-specific AGENTS.md
	codexSpecific, _ := os.ReadFile(codexAgentsPath)

	// Concatenate and write
	combined := append(shared, '\n', '\n')
	combined = append(combined, codexSpecific...)

	// Remove existing symlink if present
	if info, err := os.Lstat(destPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		os.Remove(destPath)
	}

	return os.WriteFile(destPath, combined, 0644)
}
