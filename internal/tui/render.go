package tui

import (
	"strings"
	"sync"

	glamour "charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
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

// Peek returns the current buffer content without clearing it.
func (sb *streamBuffer) Peek(agent string) string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	buf, ok := sb.buffers[agent]
	if !ok {
		return ""
	}
	return buf.String()
}

// Set replaces the buffer content for an agent (used after partial flush).
func (sb *streamBuffer) Set(agent, text string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	buf := &strings.Builder{}
	buf.WriteString(text)
	sb.buffers[agent] = buf
}

// mdRenderer is a shared glamour markdown renderer.
var mdRenderer *glamour.TermRenderer
var mdOnce sync.Once

// ag3ntsStyle returns a clean glamour style with legible colors.
func ag3ntsStyle() ansi.StyleConfig {
	boolPtr := func(b bool) *bool { return &b }
	strPtr := func(s string) *string { return &s }
	uintPtr := func(u uint) *uint { return &u }

	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			Margin: uintPtr(0),
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Bold: boolPtr(true)},
		},
		H1: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Color: strPtr("#61AFEF"), Bold: boolPtr(true)},
		},
		H2: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Color: strPtr("#61AFEF"), Bold: boolPtr(true)},
		},
		H3: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{Color: strPtr("#56B6C2"), Bold: boolPtr(true)},
		},
		Strong: ansi.StylePrimitive{Color: strPtr("#E5C07B"), Bold: boolPtr(true)},
		Emph:   ansi.StylePrimitive{Color: strPtr("#C678DD"), Italic: boolPtr(true)},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:           strPtr("#98C379"),
				BackgroundColor: strPtr("#2D2D2D"),
			},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color: strPtr("#ABB2BF"),
				},
				Margin: uintPtr(1),
			},
			Chroma: &ansi.Chroma{
				Text:            ansi.StylePrimitive{Color: strPtr("#ABB2BF")},
				Keyword:         ansi.StylePrimitive{Color: strPtr("#C678DD")},
				KeywordReserved: ansi.StylePrimitive{Color: strPtr("#C678DD")},
				KeywordType:     ansi.StylePrimitive{Color: strPtr("#E5C07B")},
				Name:            ansi.StylePrimitive{Color: strPtr("#E06C75")},
				NameBuiltin:     ansi.StylePrimitive{Color: strPtr("#E5C07B")},
				NameFunction:    ansi.StylePrimitive{Color: strPtr("#61AFEF")},
				NameClass:       ansi.StylePrimitive{Color: strPtr("#E5C07B")},
				LiteralString:   ansi.StylePrimitive{Color: strPtr("#98C379")},
				LiteralNumber:   ansi.StylePrimitive{Color: strPtr("#D19A66")},
				Operator:        ansi.StylePrimitive{Color: strPtr("#56B6C2")},
				Comment:         ansi.StylePrimitive{Color: strPtr("#5C6370"), Italic: boolPtr(true)},
				Punctuation:     ansi.StylePrimitive{Color: strPtr("#ABB2BF")},
			},
		},
		Link:     ansi.StylePrimitive{Color: strPtr("#61AFEF"), Underline: boolPtr(true)},
		LinkText: ansi.StylePrimitive{Color: strPtr("#61AFEF")},
		List: ansi.StyleList{
			StyleBlock: ansi.StyleBlock{Indent: uintPtr(2)},
		},
		Item:            ansi.StylePrimitive{},
		HorizontalRule:  ansi.StylePrimitive{Color: strPtr("#546E7A"), Format: "--------"},
		Table:           ansi.StyleTable{},
		Paragraph:       ansi.StyleBlock{},
		BlockQuote:      ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: strPtr("#546E7A")}, Indent: uintPtr(1)},
		Enumeration:     ansi.StylePrimitive{},
		Text:            ansi.StylePrimitive{},
		Strikethrough:   ansi.StylePrimitive{CrossedOut: boolPtr(true)},
	}
}

// renderMarkdown converts markdown text to styled terminal output.
func renderMarkdown(text string) string {
	mdOnce.Do(func() {
		r, err := glamour.NewTermRenderer(
			glamour.WithStyles(ag3ntsStyle()),
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
	"recall":            "🔍 ",
	"store":             "💾 ",
	"web_research":      "🌐 ",
	"code_task":         "💻 ",
	"implement":         "🔧 ",
	"run_command":       "$ ",
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
