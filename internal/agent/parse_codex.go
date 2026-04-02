package agent

import (
	"encoding/json"
	"time"
)

// codexEvent represents a single JSONL line from Codex CLI's --json output.
type codexEvent struct {
	Type     string      `json:"type"`
	ThreadID string      `json:"thread_id,omitempty"`
	Item     *codexItem  `json:"item,omitempty"`
	Usage    *codexUsage `json:"usage,omitempty"`
}

type codexItem struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Command string `json:"command,omitempty"`
	Status  string `json:"status,omitempty"`
}

type codexUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

// ParseCodex parses a single JSONL line from Codex CLI's --json output
// into a normalized AgentEvent.
func ParseCodex(line []byte, agentName, sessionID, taskID string) *AgentEvent {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}

	var ce codexEvent
	_ = json.Unmarshal(line, &ce)

	now := time.Now()
	base := AgentEvent{
		Agent:     agentName,
		SessionID: sessionID,
		TaskID:    taskID,
		Timestamp: now,
		Metadata:  raw,
	}

	switch ce.Type {
	case "thread.started":
		base.Kind = EventInit
		base.Content = ce.ThreadID
		if ce.ThreadID != "" {
			base.SessionID = ce.ThreadID
		}

	case "turn.started":
		base.Kind = EventProgress
		base.Content = "Turn started"

	case "turn.completed":
		base.Kind = EventComplete
		if ce.Usage != nil {
			base.Usage = &TokenUsage{
				InputTokens:  ce.Usage.InputTokens,
				OutputTokens: ce.Usage.OutputTokens,
				CachedTokens: ce.Usage.CachedInputTokens,
			}
		}

	case "turn.failed":
		base.Kind = EventError
		base.Content = "Turn failed"

	case "item.started", "item.completed":
		if ce.Item == nil {
			return nil
		}
		base = mapCodexItem(base, ce.Item)

	case "error":
		base.Kind = EventError
		base.Content = string(line)

	default:
		base.Kind = EventMessage
		base.Content = string(line)
	}

	return &base
}

// mapCodexItem maps a Codex item type to the appropriate AgentEvent kind.
func mapCodexItem(base AgentEvent, item *codexItem) AgentEvent {
	switch item.Type {
	case "agent_message":
		base.Kind = EventMessage
		base.Content = item.Text
	case "reasoning":
		base.Kind = EventReasoning
		base.Content = item.Text
	case "command_execution":
		base.Kind = EventCommand
		base.Content = item.Command
	case "file_change":
		base.Kind = EventFileChange
		base.Content = item.Text
	case "mcp_tool_call":
		base.Kind = EventToolUse
		base.Content = item.Text
	case "web_search":
		base.Kind = EventToolUse
		base.Content = "web_search"
	default:
		base.Kind = EventMessage
		base.Content = item.Text
	}
	return base
}
