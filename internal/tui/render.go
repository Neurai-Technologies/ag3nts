package tui

import (
	"strings"
	"sync"

	glamour "charm.land/glamour/v2"
)

// streamBuffer accumulates streaming text deltas per agent and renders
// the complete message as markdown when flushed.
type streamBuffer struct {
	mu      sync.Mutex
	buffers map[string]*strings.Builder // agentName → accumulated text
}

func newStreamBuffer() *streamBuffer {
	return &streamBuffer{
		buffers: make(map[string]*strings.Builder),
	}
}

// Append adds a text delta to the agent's buffer.
func (sb *streamBuffer) Append(agent, text string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	buf, ok := sb.buffers[agent]
	if !ok {
		buf = &strings.Builder{}
		sb.buffers[agent] = buf
	}
	buf.WriteString(text)
}

// Flush returns the accumulated text for an agent and clears the buffer.
func (sb *streamBuffer) Flush(agent string) string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	buf, ok := sb.buffers[agent]
	if !ok {
		return ""
	}
	text := buf.String()
	delete(sb.buffers, agent)
	return text
}

// Has returns true if the agent has buffered content.
func (sb *streamBuffer) Has(agent string) bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	buf, ok := sb.buffers[agent]
	return ok && buf.Len() > 0
}

// mdRenderer is a shared glamour markdown renderer.
var mdRenderer *glamour.TermRenderer
var mdOnce sync.Once

// renderMarkdown converts markdown text to styled terminal output.
func renderMarkdown(text string) string {
	mdOnce.Do(func() {
		r, err := glamour.NewTermRenderer(
			glamour.WithEnvironmentConfig(),
			glamour.WithWordWrap(100),
		)
		if err != nil {
			return
		}
		mdRenderer = r
	})

	if mdRenderer == nil {
		return text
	}

	rendered, err := mdRenderer.Render(text)
	if err != nil {
		return text
	}

	// Trim trailing whitespace glamour adds.
	return strings.TrimRight(rendered, "\n ")
}

// Tool use icons matching Claude Code's style.
var toolIcons = map[string]string{
	"Bash":              "$ ",
	"Read":              "📄 ",
	"Write":             "✏️  ",
	"Edit":              "📝 ",
	"Glob":              "🔎 ",
	"Grep":              "🔎 ",
	"Agent":             "🤖 ",
	"WebSearch":         "🌐 ",
	"WebFetch":          "🌐 ",
	"read_file":         "📄 ",
	"write_file":        "✏️  ",
	"edit_file":         "📝 ",
	"run_shell_command": "$ ",
	"shell":             "$ ",
	"search_files":      "🔎 ",
	"grep_search":       "🔎 ",
	"list_directory":    "📁 ",
	"google_web_search": "🌐 ",
	"save_memory":       "💾 ",
	"cli_help":          "❓ ",
}

// formatToolLine returns an icon-prefixed tool use description.
func formatToolLine(content string) string {
	// Content is "ToolName: details" or just "ToolName".
	name := content
	details := ""
	if idx := strings.Index(content, ": "); idx >= 0 {
		name = content[:idx]
		details = content[idx+2:]
	}

	icon := toolIcons[name]
	if icon == "" {
		icon = "⚙️  "
	}

	if details != "" {
		return icon + dimStyle.Render(name) + " " + details
	}
	return icon + dimStyle.Render(name)
}
