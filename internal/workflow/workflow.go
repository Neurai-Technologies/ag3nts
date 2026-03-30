package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/ui"
)

// SR-4: Workflow name must match safe pattern — no path separators or traversal
var validWorkflowName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func validateName(name string) error {
	if !validWorkflowName.MatchString(name) {
		return fmt.Errorf("invalid workflow name %q (must match [a-zA-Z0-9_-]+, no path separators)", name)
	}
	return nil
}

// Install clones a workflow repo and links its shared configs into tool config dirs.
func Install(name, repoURL, branch string, layout *paths.Layout, cfg *config.Config) error {
	if err := validateName(name); err != nil {
		return err
	}
	if branch == "" {
		branch = "main"
	}

	workflowDir := layout.WorkflowDir(name)

	// Clone if not already present
	if _, err := os.Stat(filepath.Join(workflowDir, ".git")); os.IsNotExist(err) {
		ui.Step(fmt.Sprintf("Cloning workflow %q from %s...", name, repoURL))
		if err := os.MkdirAll(filepath.Dir(workflowDir), 0755); err != nil {
			return fmt.Errorf("create workflows dir: %w", err)
		}
		cmd := exec.Command("git", "clone", "--branch", branch, "--single-branch", repoURL, workflowDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git clone: %w\n%s", err, string(out))
		}
		ui.OK(fmt.Sprintf("Cloned %s", name))
	} else {
		ui.Skip(fmt.Sprintf("Workflow %q already cloned", name))
	}

	// Validate marker file
	sharedDir := filepath.Join(workflowDir, "shared")
	markerPath := filepath.Join(sharedDir, "ag3nts.md")
	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		return fmt.Errorf("workflow %q missing shared/ag3nts.md marker file", name)
	}

	// Link shared configs into tool config dirs
	if err := linkConfigs(sharedDir, layout); err != nil {
		return fmt.Errorf("link configs: %w", err)
	}

	// Write MCP server config into Claude's settings.json
	if err := configureMCP(layout); err != nil {
		ui.Fail(fmt.Sprintf("MCP config: %v (can be fixed later with ag3nts doctor)", err))
	}

	// Register workflow in config
	if cfg.Workflows.Workflows == nil {
		cfg.Workflows.Workflows = make(map[string]config.WorkflowEntry)
	}
	cfg.Workflows.Active = name
	cfg.Workflows.Workflows[name] = config.WorkflowEntry{
		Repo:        repoURL,
		Branch:      branch,
		InstalledAt: time.Now(),
	}
	if err := cfg.Save(layout.ConfigFile()); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	ui.OK(fmt.Sprintf("Workflow %q active", name))
	return nil
}

// Update runs git pull on the workflow repo and re-links configs.
func Update(name string, layout *paths.Layout) error {
	workflowDir := layout.WorkflowDir(name)
	if _, err := os.Stat(filepath.Join(workflowDir, ".git")); os.IsNotExist(err) {
		return fmt.Errorf("workflow %q not found at %s", name, workflowDir)
	}

	ui.Step(fmt.Sprintf("Updating workflow %q...", name))
	cmd := exec.Command("git", "-C", workflowDir, "pull", "--ff-only")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git pull: %w\n%s", err, string(out))
	}

	// Re-link configs
	sharedDir := filepath.Join(workflowDir, "shared")
	if err := linkConfigs(sharedDir, layout); err != nil {
		return fmt.Errorf("re-link configs: %w", err)
	}

	// Re-inject MCP config (linkConfigs may re-symlink settings.json, losing MCP config)
	if err := configureMCP(layout); err != nil {
		ui.Fail(fmt.Sprintf("MCP config re-injection: %v", err))
	}

	ui.OK(fmt.Sprintf("Workflow %q updated", name))
	return nil
}

// List returns installed workflow names.
func List(layout *paths.Layout) ([]string, error) {
	workflowsDir := filepath.Join(layout.Config, "workflows")
	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// linkConfigs creates symlinks from shared/ into each tool's config dir.
func linkConfigs(sharedDir string, layout *paths.Layout) error {
	ui.Step("Linking shared configs...")

	links := []struct {
		src  string
		dest string
	}{
		// Claude Code
		{filepath.Join(sharedDir, "ag3nts.md"), filepath.Join(layout.ConfigDir("claude"), "ag3nts.md")},
		{filepath.Join(sharedDir, "claude-code", "CLAUDE.md"), filepath.Join(layout.ConfigDir("claude"), "CLAUDE.md")},
		{filepath.Join(sharedDir, "claude-code", "settings.json"), filepath.Join(layout.ConfigDir("claude"), "settings.json")},
		{filepath.Join(sharedDir, "claude-code", "files", "agents"), filepath.Join(layout.ConfigDir("claude"), "agents")},

		// Codex CLI
		{filepath.Join(sharedDir, "codex-cli", "AGENTS.md"), filepath.Join(layout.ConfigDir("codex"), "AGENTS.md")},
		{filepath.Join(sharedDir, "codex-cli", "config.toml"), filepath.Join(layout.ConfigDir("codex"), "config.toml")},

		// Gemini CLI
		{filepath.Join(sharedDir, "ag3nts.md"), filepath.Join(layout.ConfigDir("gemini"), "ag3nts.md")},
		{filepath.Join(sharedDir, "gemini-cli", "GEMINI.md"), filepath.Join(layout.ConfigDir("gemini"), "GEMINI.md")},
		{filepath.Join(sharedDir, "gemini-cli", "settings.json"), filepath.Join(layout.ConfigDir("gemini"), "settings.json")},
	}

	linked := 0
	for _, l := range links {
		// Skip if source doesn't exist in workflow
		if _, err := os.Stat(l.src); os.IsNotExist(err) {
			continue
		}

		// Ensure parent dir exists
		if err := os.MkdirAll(filepath.Dir(l.dest), 0755); err != nil {
			return err
		}

		// Remove existing symlink/file at dest
		if info, err := os.Lstat(l.dest); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || info.Mode().IsRegular() {
				os.Remove(l.dest)
			} else if info.IsDir() {
				os.RemoveAll(l.dest)
			}
		}

		// Create symlink
		if err := os.Symlink(l.src, l.dest); err != nil {
			ui.Fail(fmt.Sprintf("link %s: %v", filepath.Base(l.dest), err))
			continue
		}
		linked++
	}

	ui.OK(fmt.Sprintf("Linked %d config files", linked))
	return nil
}

// configureMCP writes the ag3nts MCP server config into Claude's settings.json.
func configureMCP(layout *paths.Layout) error {
	settingsPath := filepath.Join(layout.ConfigDir("claude"), "settings.json")

	// Read existing settings or start fresh
	settings := make(map[string]interface{})
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			// If existing settings are invalid JSON, we need the workflow's version
			// The symlink to workflow settings.json should handle this
			return nil
		}
	}

	// Find the ag3nts binary
	ag3ntsBin := filepath.Join(layout.Bin, "ag3nts")
	if _, err := os.Stat(ag3ntsBin); os.IsNotExist(err) {
		// Try current executable
		if exe, err := os.Executable(); err == nil {
			ag3ntsBin = exe
		}
	}

	// Merge MCP server config
	mcpServers, ok := settings["mcpServers"].(map[string]interface{})
	if !ok {
		mcpServers = make(map[string]interface{})
	}

	mcpServers["ag3nts"] = map[string]interface{}{
		"command": ag3ntsBin,
		"args":    []string{"mcp", "serve"},
	}
	settings["mcpServers"] = mcpServers

	// Check if settings.json is a symlink (from workflow linking)
	// If so, we need to write a separate MCP config or modify the approach
	if info, err := os.Lstat(settingsPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		// Settings is symlinked to workflow — read the symlinked file, merge, write to a new file
		// Remove symlink and write merged version
		target, _ := os.Readlink(settingsPath)
		if data, err := os.ReadFile(target); err == nil {
			if err := json.Unmarshal(data, &settings); err == nil {
				mcpServers, ok := settings["mcpServers"].(map[string]interface{})
				if !ok {
					mcpServers = make(map[string]interface{})
				}
				mcpServers["ag3nts"] = map[string]interface{}{
					"command": ag3ntsBin,
					"args":    []string{"mcp", "serve"},
				}
				settings["mcpServers"] = mcpServers
			}
		}
		os.Remove(settingsPath) // remove symlink, write real file
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	// Also write MCP config path for reference
	mcpNote := fmt.Sprintf("MCP server: %s mcp serve", ag3ntsBin)
	_ = mcpNote

	ui.OK("MCP server configured in Claude settings")
	return nil
}

// HasWorkflow checks if any workflow is installed.
func HasWorkflow(layout *paths.Layout) bool {
	names, _ := List(layout)
	return len(names) > 0
}

// ActiveWorkflowSharedDir returns the shared/ directory of the active workflow.
func ActiveWorkflowSharedDir(layout *paths.Layout, cfg *config.Config) string {
	if cfg.Workflows.Active == "" {
		return ""
	}
	return filepath.Join(layout.WorkflowDir(cfg.Workflows.Active), "shared")
}

// CountAssets counts agents and pipeline stages in a workflow's shared dir.
func CountAssets(sharedDir string) (agents, stages int) {
	agentDir := filepath.Join(sharedDir, "claude-code", "files", "agents")
	if entries, err := os.ReadDir(agentDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				agents++
			}
		}
	}

	pipelineDir := filepath.Join(sharedDir, "claude-code", "files", "pipeline")
	if entries, err := os.ReadDir(pipelineDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				stages++
			}
		}
	}

	return
}
