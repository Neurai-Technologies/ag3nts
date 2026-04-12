package context

import (
	"fmt"
	"strings"
	"time"
)

// RenderRelevant retrieves chunks matching the query and formats them
// as a prompt-ready string. Returns an empty string if no matches or
// if the store is disabled.
//
// Format:
//
//	=== m3m0ry: recent relevant context ===
//	[T<task_id> <agent> <ts>] <content>
//	...
//	=== end m3m0ry ===
func (r *RollingStore) RenderRelevant(query string) string {
	if !r.cfg.Enabled {
		return ""
	}

	chunks, err := r.Retrieve(query, time.Now())
	if err != nil || len(chunks) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("=== m3m0ry: recent relevant context ===\n")
	for _, c := range chunks {
		// Truncate very long content per chunk to keep the preview readable.
		content := c.Content
		if len(content) > 2000 {
			content = content[:2000] + "..."
		}
		taskTag := c.TaskID
		if taskTag == "" {
			taskTag = "system"
		}
		agent := c.Agent
		if agent == "" {
			agent = c.Kind
		}
		ts := c.CreatedAt.Format("15:04:05")
		b.WriteString(fmt.Sprintf("[%s %s %s]\n%s\n\n", taskTag, agent, ts, content))
	}
	b.WriteString("=== end m3m0ry ===")
	return b.String()
}
