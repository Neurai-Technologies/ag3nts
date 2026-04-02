package agent

import (
	"encoding/json"
	"time"
)

// ParseClaude parses a single JSON line from Claude Code's --output-format stream-json
// into a normalized AgentEvent.
//
// Claude stream-json event types:
//   - system: {"type":"system","subtype":"init","session_id":"...","message":"..."}
//   - assistant: {"type":"assistant","message":{"content":[{"type":"text","text":"..."}],...}}
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
		base.Kind = EventMessage
		// Content is nested: message.content[].text
		base.Content = extractAssistantText(raw)

	case "user":
		base.Kind = EventMessage
		base.Content, _ = raw["content"].(string)

	case "result":
		base.Kind = EventComplete
		base.Content, _ = raw["result"].(string)
		base.Usage = extractClaudeUsage(raw)

	case "rate_limit_event":
		// Skip rate limit events — not useful for display.
		return nil

	default:
		// Unknown event type — skip silently to avoid raw JSON in output.
		return nil
	}

	return &base
}

// extractAssistantText pulls text from the nested Claude assistant message format:
// {"message": {"content": [{"type": "text", "text": "..."}]}}
func extractAssistantText(raw map[string]any) string {
	msg, ok := raw["message"].(map[string]any)
	if !ok {
		return ""
	}

	content, ok := msg["content"].([]any)
	if !ok {
		return ""
	}

	var text string
	for _, block := range content {
		b, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := b["type"].(string); ok && t == "text" {
			if txt, ok := b["text"].(string); ok {
				text += txt
			}
		}
	}
	return text
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
