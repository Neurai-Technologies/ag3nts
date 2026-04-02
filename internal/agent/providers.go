package agent

import (
	"os"
	"path/filepath"

	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/tools"
)

// NewClaudeAgent creates a SubprocessAgent configured for Claude Code.
// Uses --bare for minimal startup and --output-format stream-json for structured output.
func NewClaudeAgent(layout *paths.Layout) *SubprocessAgent {
	tool := tools.Get("claude")
	return NewSubprocessAgent(SubprocessConfig{
		Name:       "claude",
		BinaryPath: tool.BinaryPath(layout),
		BaseFlags:  []string{"--output-format", "stream-json", "--verbose"},
		Parser:     ParseClaude,
		Capabilities: []string{
			"code-generation", "code-review", "analysis",
			"reasoning", "refactoring", "debugging",
		},
		Layout: layout,
	})
}

// NewGeminiAgent creates a SubprocessAgent configured for Gemini CLI.
// Uses stream-json output, yolo approval mode, and routes through Node.js.
func NewGeminiAgent(layout *paths.Layout) *SubprocessAgent {
	tool := tools.Get("gemini")
	nodeDir := layout.ToolDir("node")
	geminiDir := layout.ToolDir("gemini")
	return NewSubprocessAgent(SubprocessConfig{
		Name:       "gemini",
		BinaryPath: tool.BinaryPath(layout),
		BaseFlags:  []string{"--output-format", "stream-json", "--approval-mode", "yolo"},
		Parser:     ParseGemini,
		Capabilities: []string{
			"research", "large-context", "exploration",
			"google-ecosystem", "summarization",
		},
		Layout: layout,
		ExtraPaths: []string{
			filepath.Join(nodeDir, "bin"),
			filepath.Join(geminiDir, "bin"),
		},
	})
}

// NewCodexAgent creates a SubprocessAgent configured for Codex CLI.
// Uses exec subcommand with --json output and --full-auto for unattended execution.
// Codex is now an npm package (@openai/codex) — binary at node_modules/.bin/codex.
func NewCodexAgent(layout *paths.Layout) *SubprocessAgent {
	codexDir := layout.ToolDir("codex")
	nodeDir := layout.ToolDir("node")
	// Prefer npm-installed binary; fall back to legacy direct binary.
	binPath := filepath.Join(codexDir, "node_modules", ".bin", "codex")
	if _, err := os.Stat(binPath); err != nil {
		binPath = filepath.Join(codexDir, "codex") // legacy path
	}
	return NewSubprocessAgent(SubprocessConfig{
		Name:       "codex",
		BinaryPath: binPath,
		BaseFlags:  []string{"exec", "--json", "--full-auto"},
		PromptFlag: "_positional", // codex exec takes prompt as positional arg
		Parser:     ParseCodex,
		Capabilities: []string{
			"code-generation", "code-review",
			"implementation", "testing",
		},
		Layout: layout,
		ExtraPaths: []string{
			filepath.Join(nodeDir, "bin"),
			filepath.Join(codexDir, "node_modules", ".bin"),
		},
	})
}
