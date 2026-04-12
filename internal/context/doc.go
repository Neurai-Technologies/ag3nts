// Package context (aliased as m3m0ry in imports to avoid stdlib conflict)
// provides a rolling token-bounded window of raw session activity.
//
// Unlike store.MemoryStore which holds curated insights with explicit writes,
// this is an append-only, FIFO-evicted firehose that captures every event
// and task result as it happens. Agents query it by task description to
// retrieve recent-and-relevant context via keyword + recency ranking.
//
// Data is persisted to both SQLite (context_chunks table, evictable) and
// an append-only JSONL file at state/m3m0ry.jsonl (permanent audit log).
// When the token budget is exceeded, oldest chunks are evicted from SQLite.
// The JSONL file is never truncated.
//
// Typical usage:
//
//	rs, err := m3m0ry.Open(cfg, db, sessionID, logger)
//	defer rs.Close()
//	rs.Append(&m3m0ry.Chunk{Kind: "task_result", Content: output, ...})
//	relevant := rs.RenderRelevant("what did we learn about foo")
//
// Import path:
//
//	import m3m0ry "github.com/rohanrgit/ag3nts/internal/context"
package context
