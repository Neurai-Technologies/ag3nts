package context

import (
	"context"
	"time"
)

// EmbedContext returns a context with a 30-second timeout for embedding calls.
func EmbedContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// Chunk represents one entry in the rolling context window.
// It can be a completed task result, an agent event, a user message,
// or a system-generated marker.
type Chunk struct {
	ID         int64       `json:"id"`
	SessionID  string      `json:"session_id"`
	TaskID     string      `json:"task_id,omitempty"`
	Agent      string      `json:"agent,omitempty"`
	Kind       string      `json:"kind"` // "task_result", "event_message", "event_tool_use", "repair_stage_start", etc.
	Content    string      `json:"content"`
	TokenCount int         `json:"token_count"`
	Keywords   []string    `json:"keywords,omitempty"`
	Embedding  []float32   `json:"-"` // embedding vector (not serialized to JSONL)
	Seq        int64       `json:"seq"`
	CreatedAt  time.Time   `json:"created_at"`
}

// Stats reports the current state of a RollingStore.
type Stats struct {
	TotalTokens int
	ChunkCount  int
	MaxSeq      int64
	JSONLPath   string
	JSONLBytes  int64
}
