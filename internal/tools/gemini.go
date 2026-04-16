package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/ui"
)

// Gemini manages Gemini CLI installation via npm with portable Node.js.
type Gemini struct{}

// geminiPackage is the npm package name for Gemini CLI.
// TODO: verify exact package name during testing
const geminiPackage = "@google/gemini-cli"

func (g *Gemini) Name() string { return "gemini" }

func (g *Gemini) RequiredTier() config.Tier { return config.TierFree }

func (g *Gemini) Install(ctx context.Context, layout *paths.Layout) error {
	ui.Step("Installing Gemini CLI via npm...")

	if g.IsInstalled(layout) {
		ver, _ := g.InstalledVersion(layout)
		ui.Skip(fmt.Sprintf("Gemini CLI already installed (%s)", ver))
		return nil
	}

	// Ensure Node.js is installed first
	nodeBin := NodeBin(layout)
	if _, err := os.Stat(nodeBin); os.IsNotExist(err) {
		return fmt.Errorf("Node.js not installed. Run InstallNode first")
	}

	// Create gemini tool directory
	geminiDir := layout.ToolDir("gemini")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		return fmt.Errorf("create gemini dir: %w", err)
	}

	// Install via npm --prefix, using isolated cache to avoid permission conflicts
	npmBin := NpmBin(layout)
	npmCache := filepath.Join(layout.ToolDir("node"), ".npm-cache")
	cmd := exec.CommandContext(ctx, npmBin, "install", "-g", "--prefix", geminiDir, "--cache", npmCache, geminiPackage)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PATH=%s:%s", filepath.Dir(nodeBin), os.Getenv("PATH")))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("npm install gemini-cli: %w\n%s", err, string(out))
	}

	ver, _ := g.InstalledVersion(layout)
	ui.OK(fmt.Sprintf("Gemini CLI %s", ver))
	return nil
}

func (g *Gemini) IsInstalled(layout *paths.Layout) bool {
	_, err := os.Stat(g.BinaryPath(layout))
	return err == nil
}

func (g *Gemini) InstalledVersion(layout *paths.Layout) (string, error) {
	binary := g.BinaryPath(layout)
	nodeBin := NodeBin(layout)
	cmd := exec.Command(nodeBin, binary, "--version")
	out, err := cmd.Output()
	if err != nil {
		// Try running directly
		cmd2 := exec.Command(binary, "--version")
		out2, err2 := cmd2.Output()
		if err2 != nil {
			return "", fmt.Errorf("gemini --version: %w", err)
		}
		return strings.TrimSpace(string(out2)), nil
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *Gemini) LatestVersion(ctx context.Context) (string, error) {
	// Try system npm first, then fall back to detecting the ag3nts project layout
	npmBin := "npm"
	if layout, err := paths.Detect(); err == nil {
		npmBin = NpmBin(layout)
	}
	out, err := exec.CommandContext(ctx, npmBin, "view", geminiPackage, "version").Output()
	if err != nil {
		return "", fmt.Errorf("npm view: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *Gemini) BinaryPath(layout *paths.Layout) string {
	// npm --prefix installs binaries to <prefix>/bin/
	return filepath.Join(layout.ToolDir("gemini"), "bin", "gemini")
}

func (g *Gemini) AuthStatus(layout *paths.Layout) (bool, error) {
	// Check for OAuth tokens in gemini config dir
	configDir := layout.ConfigDir("gemini")
	// Gemini stores tokens in its config directory
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return false, nil
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "token") || strings.Contains(e.Name(), "auth") || strings.Contains(e.Name(), "credentials") {
			return true, nil
		}
	}
	return false, nil
}

func (g *Gemini) RunAuth(ctx context.Context, layout *paths.Layout) error {
	ui.Step("Authenticating Gemini CLI (opening browser for Google OAuth)...")
	binary := g.BinaryPath(layout)
	cmd := exec.CommandContext(ctx, binary, "auth", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
