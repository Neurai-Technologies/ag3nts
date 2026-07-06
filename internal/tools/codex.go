package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/ui"
)

// Codex manages Codex CLI installation via GitHub Releases.
type Codex struct{}

const codexRepo = "openai/codex"

func (c *Codex) Name() string { return "codex" }

func (c *Codex) RequiredTier() config.Tier { return config.TierPaid }

func (c *Codex) Install(ctx context.Context, layout *paths.Layout) error {
	ui.Step("Installing Codex CLI from GitHub Releases...")

	if c.IsInstalled(layout) {
		ver, _ := c.InstalledVersion(layout)
		ui.Skip(fmt.Sprintf("Codex CLI already installed (%s)", ver))
		return nil
	}

	// Get latest release
	asset, tag, err := c.findReleaseAsset(ctx)
	if err != nil {
		return fmt.Errorf("find codex release: %w", err)
	}

	// Create tool directory
	toolDir := layout.ToolDir("codex")
	if err := os.MkdirAll(toolDir, 0755); err != nil {
		return fmt.Errorf("create codex dir: %w", err)
	}

	// Download binary
	ui.Step(fmt.Sprintf("Downloading Codex CLI %s...", tag))
	binaryPath := filepath.Join(toolDir, "codex")
	if err := downloadFile(ctx, asset, binaryPath); err != nil {
		return fmt.Errorf("download codex: %w", err)
	}

	// Make executable
	if err := os.Chmod(binaryPath, 0755); err != nil {
		return fmt.Errorf("chmod codex: %w", err)
	}

	// Strip quarantine
	stripQuarantine(binaryPath)

	ui.OK(fmt.Sprintf("Codex CLI %s", tag))
	return nil
}

func (c *Codex) IsInstalled(layout *paths.Layout) bool {
	_, err := os.Stat(c.BinaryPath(layout))
	return err == nil
}

func (c *Codex) InstalledVersion(layout *paths.Layout) (string, error) {
	out, err := exec.Command(c.BinaryPath(layout), "--version").Output()
	if err != nil {
		return "", fmt.Errorf("codex --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Codex) LatestVersion(ctx context.Context) (string, error) {
	_, tag, err := c.findReleaseAsset(ctx)
	return tag, err
}

func (c *Codex) BinaryPath(layout *paths.Layout) string {
	return filepath.Join(layout.ToolDir("codex"), "codex")
}

func (c *Codex) AuthStatus(layout *paths.Layout) (bool, error) {
	// Check for auth.json in codex config dir
	authFile := filepath.Join(layout.ConfigDir("codex"), "auth.json")
	_, err := os.Stat(authFile)
	return err == nil, nil
}

func (c *Codex) RunAuth(ctx context.Context, layout *paths.Layout) error {
	ui.Step("Authenticating Codex CLI (device code flow)...")
	cmd := exec.CommandContext(ctx, c.BinaryPath(layout), "auth")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// findReleaseAsset queries GitHub API for the latest release and finds the
// darwin-arm64 binary asset.
func (c *Codex) findReleaseAsset(ctx context.Context) (downloadURL, tag string, err error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", codexRepo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("github api returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", fmt.Errorf("parse release: %w", err)
	}

	// Look for darwin-arm64 asset
	arch := runtime.GOARCH // arm64
	patterns := []string{
		fmt.Sprintf("darwin-%s", arch),
		fmt.Sprintf("macos-%s", arch),
		"apple-darwin",
		"aarch64-apple",
	}

	for _, asset := range release.Assets {
		nameLower := strings.ToLower(asset.Name)
		for _, pat := range patterns {
			if strings.Contains(nameLower, pat) {
				return asset.BrowserDownloadURL, release.TagName, nil
			}
		}
	}

	return "", "", fmt.Errorf("no darwin-arm64 asset found in release %s", release.TagName)
}

// downloadFile downloads a URL to a local file path.
func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}
