package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Layout holds all canonical paths for an ag3nts installation.
type Layout struct {
	Base   string // root project directory
	Bin    string // bin/ — ag3nts binary
	Tools  string // tools/ — installed tool binaries
	Config string // config/ — ag3nts.toml, auth, workflows, tool configs
	Cache  string // cache/ — rollback binaries
	State  string // state/ — versions.toml, timestamps
}

// markerFile is the file that identifies an ag3nts project directory.
const markerFile = "shared/ag3nts.md"

// Detect locates the ag3nts project directory by scanning:
//  1. Current working directory (and parents)
//  2. Mounted volumes under /Volumes (SSD detection)
//  3. Home directory
func Detect() (*Layout, error) {
	// 1. Walk up from cwd
	if cwd, err := os.Getwd(); err == nil {
		if base := walkUp(cwd); base != "" {
			return newLayout(base), nil
		}
	}

	// 2. Scan /Volumes for SSD
	entries, err := os.ReadDir("/Volumes")
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			// Look for common paths on mounted volumes
			candidates := []string{
				filepath.Join("/Volumes", e.Name(), "SourceCodes", "Products", "ag3nts"),
				filepath.Join("/Volumes", e.Name(), "ag3nts"),
			}
			for _, c := range candidates {
				if hasMarker(c) {
					return newLayout(c), nil
				}
			}
		}
	}

	// 3. Home directory
	if home, err := os.UserHomeDir(); err == nil {
		candidates := []string{
			filepath.Join(home, "ag3nts"),
			filepath.Join(home, "Projects", "ag3nts"),
			filepath.Join(home, "SourceCodes", "Products", "ag3nts"),
		}
		for _, c := range candidates {
			if hasMarker(c) {
				return newLayout(c), nil
			}
		}
	}

	return nil, fmt.Errorf("ag3nts project directory not found (looking for %s)", markerFile)
}

// FromPath creates a Layout from an explicit base path.
func FromPath(base string) (*Layout, error) {
	if !hasMarker(base) {
		return nil, fmt.Errorf("%s not found in %s", markerFile, base)
	}
	return newLayout(base), nil
}

// EnsureDirs creates all canonical directories if they don't exist.
func (l *Layout) EnsureDirs() error {
	dirs := []string{l.Bin, l.Tools, l.Config, l.Cache, l.State}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

// ToolDir returns the path for a specific tool under tools/.
func (l *Layout) ToolDir(name string) string {
	return filepath.Join(l.Tools, name)
}

// ConfigDir returns the path for a specific tool's config under config/.
func (l *Layout) ConfigDir(name string) string {
	return filepath.Join(l.Config, name)
}

// WorkflowDir returns the path for a named workflow under config/workflows/.
func (l *Layout) WorkflowDir(name string) string {
	return filepath.Join(l.Config, "workflows", name)
}

// ConfigFile returns the path to config/ag3nts.toml.
func (l *Layout) ConfigFile() string {
	return filepath.Join(l.Config, "ag3nts.toml")
}

// StateFile returns the path to state/versions.toml.
func (l *Layout) StateFile() string {
	return filepath.Join(l.State, "versions.toml")
}

func newLayout(base string) *Layout {
	return &Layout{
		Base:   base,
		Bin:    filepath.Join(base, "bin"),
		Tools:  filepath.Join(base, "tools"),
		Config: filepath.Join(base, "config"),
		Cache:  filepath.Join(base, "cache"),
		State:  filepath.Join(base, "state"),
	}
}

func hasMarker(base string) bool {
	info, err := os.Stat(filepath.Join(base, markerFile))
	return err == nil && !info.IsDir()
}

// walkUp climbs from dir toward /, checking each directory for the marker file.
func walkUp(dir string) string {
	for {
		if hasMarker(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		// Don't walk above /Volumes or home
		if parent == "/" || strings.Count(dir, string(filepath.Separator)) <= 1 {
			break
		}
		dir = parent
	}
	return ""
}
