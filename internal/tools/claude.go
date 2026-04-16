package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/ui"
)

// Claude manages Claude Code installation via Homebrew cask.
type Claude struct{}

func (c *Claude) Name() string { return "claude" }

func (c *Claude) RequiredTier() config.Tier { return config.TierPremium }

func (c *Claude) Install(ctx context.Context, layout *paths.Layout) error {
	ui.Step("Installing Claude Code via Homebrew...")

	// Check if brew is available
	if _, err := exec.LookPath("brew"); err != nil {
		return fmt.Errorf("Homebrew not found. Install it from https://brew.sh then retry")
	}

	// Check if already installed
	if c.IsInstalled(layout) {
		ver, _ := c.InstalledVersion(layout)
		ui.Skip(fmt.Sprintf("Claude Code already installed (%s)", ver))
		return nil
	}

	cmd := exec.CommandContext(ctx, "brew", "install", "--cask", "claude")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("brew install --cask claude: %w", err)
	}

	// Strip quarantine
	binary := c.BinaryPath(layout)
	stripQuarantine(binary)

	ver, _ := c.InstalledVersion(layout)
	ui.OK(fmt.Sprintf("Claude Code %s", ver))
	return nil
}

func (c *Claude) IsInstalled(layout *paths.Layout) bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

func (c *Claude) InstalledVersion(layout *paths.Layout) (string, error) {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("claude --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Claude) LatestVersion(ctx context.Context) (string, error) {
	// brew info --cask claude --json outputs version info
	out, err := exec.CommandContext(ctx, "brew", "info", "--cask", "claude", "--json=v2").Output()
	if err != nil {
		return "", fmt.Errorf("brew info: %w", err)
	}
	// Parse version from JSON — simplified, extract version field
	s := string(out)
	if idx := strings.Index(s, `"version":"`); idx >= 0 {
		s = s[idx+len(`"version":"`):]
		if end := strings.Index(s, `"`); end >= 0 {
			return s[:end], nil
		}
	}
	return "", fmt.Errorf("could not parse version from brew info")
}

func (c *Claude) BinaryPath(layout *paths.Layout) string {
	// Claude Code is installed by brew, typically in PATH
	path, err := exec.LookPath("claude")
	if err != nil {
		return "claude" // fallback to PATH lookup at runtime
	}
	return path
}

func (c *Claude) AuthStatus(layout *paths.Layout) (bool, error) {
	// Check if claude is authenticated by running a quick command
	cmd := exec.Command("claude", "--version")
	err := cmd.Run()
	// If claude runs at all, auth may still be needed — but binary works
	// Full auth check would require trying an actual API call
	return err == nil, nil
}

func (c *Claude) RunAuth(ctx context.Context, layout *paths.Layout) error {
	ui.Step("Authenticating Claude Code (opening browser)...")
	cmd := exec.CommandContext(ctx, "claude", "login")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
