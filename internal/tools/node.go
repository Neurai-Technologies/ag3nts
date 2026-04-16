package tools

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/ui"
)

// InstallNode downloads and extracts a portable Node.js into tools/node/.
func InstallNode(ctx context.Context, layout *paths.Layout, version string) error {
	nodeDir := layout.ToolDir("node")
	nodeBin := filepath.Join(nodeDir, "bin", "node")

	// Check if already installed
	if _, err := os.Stat(nodeBin); err == nil {
		ui.Skip(fmt.Sprintf("Node.js already installed at %s", nodeDir))
		return nil
	}

	ui.Step(fmt.Sprintf("Downloading Node.js %s (darwin-arm64)...", version))

	url := fmt.Sprintf("https://nodejs.org/dist/v%s/node-v%s-darwin-arm64.tar.gz", version, version)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download node.js: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("node.js download returned %d", resp.StatusCode)
	}

	// Extract tar.gz
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		return fmt.Errorf("create node dir: %w", err)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	prefix := fmt.Sprintf("node-v%s-darwin-arm64/", version)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		// Strip the top-level directory prefix
		name := hdr.Name
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		name = strings.TrimPrefix(name, prefix)
		if name == "" {
			continue
		}

		// SR-2: Path traversal prevention — validate extracted path stays within nodeDir
		cleanName := filepath.Clean(name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			return fmt.Errorf("tar contains path traversal entry: %s", name)
		}
		target := filepath.Join(nodeDir, cleanName)
		if !strings.HasPrefix(target, filepath.Clean(nodeDir)+string(filepath.Separator)) && target != filepath.Clean(nodeDir) {
			return fmt.Errorf("tar entry escapes target directory: %s", name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("extract %s: %w", target, err)
			}
			f.Close()
		case tar.TypeSymlink:
			// SR-2: Validate symlink target stays within nodeDir
			linkTarget := filepath.Join(filepath.Dir(target), hdr.Linkname)
			linkTarget = filepath.Clean(linkTarget)
			if !strings.HasPrefix(linkTarget, filepath.Clean(nodeDir)+string(filepath.Separator)) && linkTarget != filepath.Clean(nodeDir) {
				return fmt.Errorf("tar symlink escapes target directory: %s -> %s", name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("symlink %s: %w", target, err)
			}
		}
	}

	ui.OK(fmt.Sprintf("Node.js %s", version))
	return nil
}

// NodeBin returns the path to the node binary.
func NodeBin(layout *paths.Layout) string {
	return filepath.Join(layout.ToolDir("node"), "bin", "node")
}

// NpmBin returns the path to the npm binary.
func NpmBin(layout *paths.Layout) string {
	return filepath.Join(layout.ToolDir("node"), "bin", "npm")
}
