package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/tools"
	"github.com/rohanrgit/ag3nts/internal/ui"
)

// UpdateInfo holds version comparison for a tool.
type UpdateInfo struct {
	Tool      string
	Current   string
	Latest    string
	Available bool
}

// Check compares installed vs latest version for a tool.
func Check(ctx context.Context, tool tools.Tool, layout *paths.Layout) (*UpdateInfo, error) {
	current, err := tool.InstalledVersion(layout)
	if err != nil {
		return nil, fmt.Errorf("installed version: %w", err)
	}

	latest, err := tool.LatestVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("latest version: %w", err)
	}

	return &UpdateInfo{
		Tool:      tool.Name(),
		Current:   current,
		Latest:    latest,
		Available: current != latest,
	}, nil
}

// Apply updates a tool: caches current binary, installs new version.
func Apply(ctx context.Context, tool tools.Tool, layout *paths.Layout) error {
	// Cache current binary for rollback
	if err := cacheCurrent(tool, layout); err != nil {
		ui.Fail(fmt.Sprintf("cache %s for rollback: %v (continuing with update)", tool.Name(), err))
	}

	// Re-install (which downloads latest)
	return tool.Install(ctx, layout)
}

// Rollback restores the previous version from cache.
func Rollback(tool tools.Tool, layout *paths.Layout) error {
	name := tool.Name()
	cacheDir := filepath.Join(layout.Cache, name)

	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) == 0 {
		return fmt.Errorf("no cached version found for %s. Cannot rollback.", name)
	}

	// Use the most recent cached version
	latest := entries[len(entries)-1]
	cachedBin := filepath.Join(cacheDir, latest.Name(), name)

	if _, err := os.Stat(cachedBin); os.IsNotExist(err) {
		return fmt.Errorf("cached binary not found: %s", cachedBin)
	}

	destBin := tool.BinaryPath(layout)

	// Copy cached binary to tool location
	data, err := os.ReadFile(cachedBin)
	if err != nil {
		return fmt.Errorf("read cached binary: %w", err)
	}
	if err := os.WriteFile(destBin, data, 0755); err != nil {
		return fmt.Errorf("restore binary: %w", err)
	}

	ui.OK(fmt.Sprintf("Rolled back %s to %s", name, latest.Name()))
	return nil
}

// UpdateAll checks and updates all allowed tools.
func UpdateAll(ctx context.Context, layout *paths.Layout, cfg *config.Config) error {
	ui.Header("Checking for updates")
	updated := 0

	for _, toolName := range cfg.AllowedTools() {
		tool := tools.Get(toolName)
		if tool == nil || !tool.IsInstalled(layout) {
			continue
		}

		info, err := Check(ctx, tool, layout)
		if err != nil {
			ui.Fail(fmt.Sprintf("%s: %v", toolName, err))
			continue
		}

		if !info.Available {
			ui.OK(fmt.Sprintf("%s %s (up to date)", toolName, info.Current))
			continue
		}

		ui.Step(fmt.Sprintf("%s %s → %s", toolName, info.Current, info.Latest))
		if err := Apply(ctx, tool, layout); err != nil {
			ui.Fail(fmt.Sprintf("update %s: %v", toolName, err))
			continue
		}
		updated++
	}

	if updated == 0 {
		fmt.Println("\nAll tools are up to date.")
	} else {
		fmt.Printf("\nUpdated %d tool(s).\n", updated)
	}
	return nil
}

func cacheCurrent(tool tools.Tool, layout *paths.Layout) error {
	name := tool.Name()
	binary := tool.BinaryPath(layout)

	if _, err := os.Stat(binary); os.IsNotExist(err) {
		return nil // nothing to cache
	}

	ver, err := tool.InstalledVersion(layout)
	if err != nil {
		ver = "unknown"
	}

	cacheDir := filepath.Join(layout.Cache, name, ver)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}

	data, err := os.ReadFile(binary)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(cacheDir, name), data, 0755)
}
