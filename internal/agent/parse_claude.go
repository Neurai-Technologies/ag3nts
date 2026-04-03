package agent

import (
	"encoding/json"
	"fmt"
	"time"
)

// ParseClaude parses a single JSON line from Claude Code's --output-format stream-json
// into one or more normalized AgentEvents.
//
// Claude stream-json event types:
//   - system: {"type":"system","subtype":"init","session_id":"...","tools":[...],...}
//   - assistant: {"type":"assistant","message":{"content":[{"type":"text|tool_use|thinking",...}],...}}
//   - user: {"type":"user","message":{"content":[{"type":"tool_result",...}],...}}
//   - result: {"type":"result","result":"...","total_cost_usd":0.1,"usage":{...}}
//   - rate_limit_event: (skip)
func ParseClaude(line []byte, agentName, sessionID, taskID string) *AgentEvent {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}

	now := time.Now()
	base := AgentEvent{
		Agent:     agentName,
		SessionID: sessionID,
		TaskID:    taskID,
		Timestamp: now,
		Metadata:  raw,
	}

	eventType, _ := raw["type"].(string)

	switch eventType {
	case "system":
		base.Kind = EventInit
		base.Content, _ = raw["message"].(string)
		if sid, ok := raw["session_id"].(string); ok && sid != "" {
			base.SessionID = sid
		}

	case "assistant":
		return parseAssistantEvent(base, raw)

	case "user":
		// User events are tool_result blocks — suppress to avoid echo.
		return nil

	case "result":
		base.Kind = EventComplete
		base.Content, _ = raw["result"].(string)
		base.Usage = extractClaudeUsage(raw)

	case "rate_limit_event":
		return nil

	default:
		return nil
	}

	return &base
}

// parseAssistantEvent handles assistant messages which contain content blocks:
// text, tool_use, and thinking.
func parseAssistantEvent(base AgentEvent, raw map[string]any) *AgentEvent {
	msg, ok := raw["message"].(map[string]any)
	if !ok {
		return nil
	}
	content, ok := msg["content"].([]any)
	if !ok {
		return nil
	}

	// Process all content blocks and combine into a single event.
	// Priority: tool_use > text > thinking (show the most actionable info).
	var textParts []string
	var toolParts []string
	var thinkingParts []string

	for _, block := range content {
		b, ok := block.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := b["type"].(string)

		switch blockType {
		case "text":
			if txt, ok := b["text"].(string); ok && txt != "" {
				textParts = append(textParts, txt)
			}
		case "tool_use":
			toolParts = append(toolParts, formatClaudeToolUse(b))
		case "thinking":
			if txt, ok := b["thinking"].(string); ok && txt != "" {
				thinkingParts = append(thinkingParts, txt)
			}
		}
	}

	// Emit tool_use events as EventToolUse.
	if len(toolParts) > 0 {
		base.Kind = EventToolUse
		combined := ""
		for i, p := range toolParts {
			if i > 0 {
				combined += "\n"
			}
			combined += p
		}
		base.Content = combined
		return &base
	}

	// Emit text as EventMessage.
	if len(textParts) > 0 {
		base.Kind = EventMessage
		combined := ""
		for _, p := range textParts {
			combined += p
		}
		base.Content = combined
		return &base
	}

	// Emit thinking as EventReasoning.
	if len(thinkingParts) > 0 {
		base.Kind = EventReasoning
		combined := ""
		for _, p := range thinkingParts {
			combined += p
		}
		base.Content = combined
		return &base
	}

	return nil
}

// formatClaudeToolUse formats a tool_use content block for display.
func formatClaudeToolUse(block map[string]any) string {
	name, _ := block["name"].(string)
	input, _ := block["input"].(map[string]any)

	if input == nil {
		return name
	}

	// Extract the most useful parameter based on tool name.
	switch name {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			return fmt.Sprintf("%s: %s", name, cmd)
		}
	case "Read":
		if path, ok := input["file_path"].(string); ok {
			return fmt.Sprintf("%s: %s", name, path)
		}
	case "Write":
		if path, ok := input["file_path"].(string); ok {
			return fmt.Sprintf("%s: %s", name, path)
		}
	case "Edit":
		if path, ok := input["file_path"].(string); ok {
			return fmt.Sprintf("%s: %s", name, path)
		}
	case "Glob":
		if pattern, ok := input["pattern"].(string); ok {
			return fmt.Sprintf("%s: %s", name, pattern)
		}
	case "Grep":
		if pattern, ok := input["pattern"].(string); ok {
			return fmt.Sprintf("%s: %s", name, pattern)
		}
	case "Agent":
		if desc, ok := input["description"].(string); ok {
			return fmt.Sprintf("%s: %s", name, desc)
		}
	case "WebSearch":
		if query, ok := input["query"].(string); ok {
			return fmt.Sprintf("%s: %s", name, query)
		}
	case "WebFetch":
		if url, ok := input["url"].(string); ok {
			return fmt.Sprintf("%s: %s", name, url)
		}
	}

	// Fallback: just show the tool name.
	return name
}

// extractClaudeUsage pulls token usage from a result event.
func extractClaudeUsage(raw map[string]any) *TokenUsage {
	usage, ok := raw["usage"].(map[string]any)
	if !ok {
		return nil
	}

	toInt := func(v any) int {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		default:
			return 0
		}
	}

	cost, _ := raw["total_cost_usd"].(float64)

	return &TokenUsage{
		InputTokens:  toInt(usage["input_tokens"]),
		OutputTokens: toInt(usage["output_tokens"]),
		CachedTokens: toInt(usage["cache_read_input_tokens"]),
		TotalCost:    cost,
	}
}
