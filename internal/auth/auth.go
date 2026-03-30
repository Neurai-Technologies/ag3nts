package auth

import (
	"context"
	"fmt"

	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/tools"
	"github.com/rohanrgit/ag3nts/internal/ui"
)

// SetupAll runs OAuth for each tool allowed by the tier.
func SetupAll(ctx context.Context, layout *paths.Layout, cfg *config.Config) error {
	ui.Header("Setting up authentication")

	allowed := cfg.AllowedTools()
	for _, toolName := range allowed {
		tool := tools.Get(toolName)
		if tool == nil {
			continue
		}

		// Check if already authenticated
		authed, _ := tool.AuthStatus(layout)
		if authed {
			ui.Skip(fmt.Sprintf("%s already authenticated", toolName))
			continue
		}

		if err := tool.RunAuth(ctx, layout); err != nil {
			ui.Fail(fmt.Sprintf("%s auth: %v", toolName, err))
			// Continue with other tools — don't fail the whole setup
		}
	}

	return nil
}

// Status checks auth status for all allowed tools.
func Status(layout *paths.Layout, cfg *config.Config) map[string]bool {
	result := make(map[string]bool)
	for _, toolName := range cfg.AllowedTools() {
		tool := tools.Get(toolName)
		if tool == nil {
			continue
		}
		authed, _ := tool.AuthStatus(layout)
		result[toolName] = authed
	}
	return result
}
