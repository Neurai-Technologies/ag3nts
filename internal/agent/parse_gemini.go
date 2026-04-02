package agent

import (
	"encoding/json"
	"time"
)

// geminiEvent represents a single line from Gemini CLI's --output-format stream-json.
type geminiEvent struct {
	Type      string       `json:"type"`
	SessionID string       `json:"session_id,omitempty"`
	Model     string       `json:"model,omitempty"`
	Role      string       `json:"role,omitempty"`
	Content   string       `json:"content,omitempty"`
	Delta     bool         `json:"delta,omitempty"`
	ToolName  string       `json:"tool_name,omitempty"`
	ToolID    string       `json:"tool_id,omitempty"`
	Params    any          `json:"parameters,omitempty"`
	Status    string       `json:"status,omitempty"`
	Output    string       `json:"output,omitempty"`
	Error     string       `json:"error,omitempty"`
	Severity  string       `json:"severity,omitempty"`
	Message   string       `json:"message,omitempty"`
	Stats     *geminiStats `json:"stats,omitempty"`
}

type geminiStats struct {
	TotalTokens  int `json:"total_tokens"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	Cached       int `json:"cached"`
}

// ParseGemini parses a single JSON line from Gemini CLI's stream-json output
// into a normalized AgentEvent.
func ParseGemini(line []byte, agentName, sessionID, taskID string) *AgentEvent {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}

	var ge geminiEvent
	_ = json.Unmarshal(line, &ge)

	now := time.Now()
	base := AgentEvent{
		Agent:     agentName,
		SessionID: sessionID,
		TaskID:    taskID,
		Timestamp: now,
		Metadata:  raw,
	}

	switch ge.Type {
	case "init":
		base.Kind = EventInit
		base.Content = ge.Model
		if ge.SessionID != "" {
			base.SessionID = ge.SessionID
		}

	case "message":
		if ge.Delta {
			base.Kind = EventProgress
		} else {
			base.Kind = EventMessage
		}
		base.Content = ge.Content

	case "tool_use":
		base.Kind = EventToolUse
		base.Content = formatToolUse(ge.ToolName, ge.Params)

	case "tool_result":
		base.Kind = EventToolResult
		base.Content = ge.Output
		if ge.Status == "error" {
			base.Content = ge.Error
		}

	case "error":
		base.Kind = EventError
		base.Content = ge.Message

	case "result":
		base.Kind = EventComplete
		base.Content = ge.Status
		if ge.Stats != nil {
			base.Usage = &TokenUsage{
				InputTokens:  ge.Stats.InputTokens,
				OutputTokens: ge.Stats.OutputTokens,
				CachedTokens: ge.Stats.Cached,
			}
		}

	default:
		base.Kind = EventMessage
		base.Content = string(line)
	}

	return &base
}

// formatToolUse formats a tool call with its parameters for display.
func formatToolUse(name string, params any) string {
	p, ok := params.(map[string]any)
	if !ok || len(p) == 0 {
		return name
	}

	// Extract the most useful parameter based on tool name.
	switch name {
	case "run_shell_command", "shell":
		if cmd, ok := p["command"].(string); ok {
			return name + ": " + cmd
		}
	case "read_file":
		if path, ok := p["path"].(string); ok {
			return name + ": " + path
		}
	case "write_file", "edit_file":
		if path, ok := p["path"].(string); ok {
			return name + ": " + path
		}
	case "search_files", "grep":
		if pattern, ok := p["pattern"].(string); ok {
			return name + ": " + pattern
		}
	case "list_directory":
		if path, ok := p["path"].(string); ok {
			return name + ": " + path
		}
	}

	// Fallback: show all param keys and short values.
	var parts []string
	for k, v := range p {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if len(s) > 80 {
			s = s[:77] + "..."
		}
		parts = append(parts, k+"="+s)
	}
	if len(parts) > 0 {
		return name + ": " + joinParts(parts)
	}
	return name
}

func joinParts(parts []string) string {
	result := parts[0]
	for _, p := range parts[1:] {
		result += ", " + p
	}
	return result
}
