package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rohanrgit/ag3nts/internal/paths"
)

const queryTimeout = 120 * time.Second

// SR-5: Only forward safe environment variables to MCP subprocesses
func filteredEnv(extraPaths ...string) []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "TERM": true,
		"LANG": true, "LC_ALL": true, "SHELL": true, "TMPDIR": true,
		"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
	}

	var env []string
	for _, e := range os.Environ() {
		key := e[:strings.IndexByte(e, '=')]
		if allowed[key] {
			if key == "PATH" && len(extraPaths) > 0 {
				env = append(env, fmt.Sprintf("PATH=%s:%s",
					strings.Join(extraPaths, ":"), os.Getenv("PATH")))
			} else {
				env = append(env, e)
			}
		}
	}

	// Ensure PATH is set even if not in original env
	hasPath := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
			break
		}
	}
	if !hasPath && len(extraPaths) > 0 {
		env = append(env, fmt.Sprintf("PATH=%s", strings.Join(extraPaths, ":")))
	}

	return env
}

// queryGemini invokes Gemini CLI in non-interactive mode and returns the response.
func queryGemini(prompt string, layout *paths.Layout) (string, error) {
	geminiDir := layout.ToolDir("gemini")
	geminibin := filepath.Join(geminiDir, "bin", "gemini")

	if _, err := os.Stat(geminibin); os.IsNotExist(err) {
		return "", fmt.Errorf("Gemini CLI not installed. Run: ag3nts install")
	}

	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	// Gemini CLI non-interactive: pass prompt via stdin with -p flag
	// Try different non-interactive invocation methods
	nodeBin := filepath.Join(layout.ToolDir("node"), "bin", "node")

	// Use node to run gemini with prompt piped to stdin
	cmd := exec.CommandContext(ctx, nodeBin, geminibin, "-p", prompt)
	cmd.Env = filteredEnv(
		filepath.Join(layout.ToolDir("node"), "bin"),
		filepath.Join(geminiDir, "bin"),
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback: try running gemini directly
		cmd2 := exec.CommandContext(ctx, geminibin, "-p", prompt)
		out2, err2 := cmd2.CombinedOutput()
		if err2 != nil {
			return "", fmt.Errorf("gemini query failed: %w\n%s", err, string(out))
		}
		return strings.TrimSpace(string(out2)), nil
	}

	return strings.TrimSpace(string(out)), nil
}

// queryCodex invokes Codex CLI in non-interactive mode and returns the response.
func queryCodex(prompt string, layout *paths.Layout) (string, error) {
	codexBin := filepath.Join(layout.ToolDir("codex"), "codex")

	if _, err := os.Stat(codexBin); os.IsNotExist(err) {
		return "", fmt.Errorf("Codex CLI not installed. Run: ag3nts install (requires paid tier)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	// Codex CLI non-interactive mode
	cmd := exec.CommandContext(ctx, codexBin, "exec", "-p", prompt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback: try --quiet or pipe mode
		cmd2 := exec.CommandContext(ctx, codexBin, "-p", prompt)
		out2, err2 := cmd2.CombinedOutput()
		if err2 != nil {
			return "", fmt.Errorf("codex query failed: %w\n%s", err, string(out))
		}
		return strings.TrimSpace(string(out2)), nil
	}

	return strings.TrimSpace(string(out)), nil
}
