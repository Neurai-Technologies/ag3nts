package tools

import (
	"context"

	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/paths"
)

// Tool defines the interface for a managed AI coding tool.
type Tool interface {
	// Name returns the tool identifier (e.g., "claude", "codex", "gemini").
	Name() string

	// Install downloads and installs the tool.
	Install(ctx context.Context, layout *paths.Layout) error

	// IsInstalled checks if the tool binary exists.
	IsInstalled(layout *paths.Layout) bool

	// InstalledVersion returns the currently installed version string.
	InstalledVersion(layout *paths.Layout) (string, error)

	// LatestVersion queries the latest available version.
	LatestVersion(ctx context.Context) (string, error)

	// BinaryPath returns the path to the tool's executable.
	BinaryPath(layout *paths.Layout) string

	// RequiredTier returns the minimum tier needed for this tool.
	RequiredTier() config.Tier

	// AuthStatus checks if the tool is authenticated.
	AuthStatus(layout *paths.Layout) (bool, error)

	// RunAuth launches the tool's native OAuth flow.
	RunAuth(ctx context.Context, layout *paths.Layout) error
}
