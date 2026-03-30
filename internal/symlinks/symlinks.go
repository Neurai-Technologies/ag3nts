package symlinks

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/ui"
)

// Spec defines a symlink: from Link to Target.
type Spec struct {
	Name   string // human-readable name (e.g., "claude")
	Link   string // symlink path (e.g., ~/.claude)
	Target string // target directory (e.g., config/claude/)
}

// AllSpecs returns the symlink specs for all three tools.
func AllSpecs(layout *paths.Layout) []Spec {
	home, _ := os.UserHomeDir()
	return []Spec{
		{Name: "claude", Link: filepath.Join(home, ".claude"), Target: layout.ConfigDir("claude")},
		{Name: "codex", Link: filepath.Join(home, ".codex"), Target: layout.ConfigDir("codex")},
		{Name: "gemini", Link: filepath.Join(home, ".gemini"), Target: layout.ConfigDir("gemini")},
	}
}

// Create creates a symlink, backing up any existing target.
func Create(spec Spec) error {
	// Ensure target directory exists
	if err := os.MkdirAll(spec.Target, 0755); err != nil {
		return fmt.Errorf("create target %s: %w", spec.Target, err)
	}

	// Check what's currently at the link path
	info, err := os.Lstat(spec.Link)
	if err == nil {
		// Something exists at the link path
		if info.Mode()&os.ModeSymlink != 0 {
			// Existing symlink — check if it already points to our target
			existing, err := os.Readlink(spec.Link)
			if err == nil && existing == spec.Target {
				ui.Skip(fmt.Sprintf("%s → %s (already correct)", spec.Link, spec.Target))
				return nil
			}
			// Points elsewhere — remove and recreate
			os.Remove(spec.Link)
		} else {
			// Regular file or directory — back it up
			backup := spec.Link + ".backup." + time.Now().Format("20060102-150405")
			ui.Step(fmt.Sprintf("Backing up existing %s to %s", spec.Link, backup))
			if err := os.Rename(spec.Link, backup); err != nil {
				return fmt.Errorf("backup %s: %w", spec.Link, err)
			}
		}
	}

	// Create symlink
	if err := os.Symlink(spec.Target, spec.Link); err != nil {
		return fmt.Errorf("symlink %s → %s: %w", spec.Link, spec.Target, err)
	}

	ui.OK(fmt.Sprintf("%s → %s", spec.Link, spec.Target))
	return nil
}

// Validate checks if a symlink exists and points to the correct target.
func Validate(spec Spec) (bool, string) {
	info, err := os.Lstat(spec.Link)
	if err != nil {
		return false, fmt.Sprintf("%s does not exist", spec.Link)
	}

	if info.Mode()&os.ModeSymlink == 0 {
		return false, fmt.Sprintf("%s exists but is not a symlink", spec.Link)
	}

	target, err := os.Readlink(spec.Link)
	if err != nil {
		return false, fmt.Sprintf("%s: cannot read symlink: %v", spec.Link, err)
	}

	if target != spec.Target {
		return false, fmt.Sprintf("%s points to %s, expected %s", spec.Link, target, spec.Target)
	}

	return true, ""
}

// CreateAll creates all symlinks for the given layout.
func CreateAll(layout *paths.Layout) error {
	ui.Header("Setting up config symlinks")
	for _, spec := range AllSpecs(layout) {
		if err := Create(spec); err != nil {
			return err
		}
	}
	return nil
}
