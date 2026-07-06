package context

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rohanrgit/ag3nts/internal/store"
)

func openTestStore(t *testing.T) (*RollingStore, *store.DB) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	_ = db.CreateSession(&store.SessionRecord{ID: "s-test", Status: "active"})

	jsonlPath := filepath.Join(t.TempDir(), "m3m0ry.jsonl")
	cfg := DefaultConfig()
	cfg.JSONLPath = jsonlPath
	cfg.MaxTokens = 1000 // small budget for eviction tests
	cfg.RetrievalLimit = 10
	cfg.RetrievalBudget = 500

	rs, err := Open(cfg, db, "s-test", nil)
	if err != nil {
		t.Fatalf("open rolling store: %v", err)
	}
	t.Cleanup(func() { rs.Close() })

	return rs, db
}

func TestRollingStoreAppend(t *testing.T) {
	rs, db := openTestStore(t)

	chunk := &Chunk{
		TaskID:  "t-1",
		Agent:   "claude",
		Kind:    "task_result",
		Content: "analyzed the codebase structure",
	}
	if err := rs.Append(chunk); err != nil {
		t.Fatalf("append: %v", err)
	}

	if chunk.ID == 0 {
		t.Error("expected ID to be set")
	}
	if chunk.Seq != 1 {
		t.Errorf("seq = %d, want 1", chunk.Seq)
	}
	if chunk.TokenCount == 0 {
		t.Error("expected token count to be computed")
	}
	if len(chunk.Keywords) == 0 {
		t.Error("expected keywords to be extracted")
	}

	// Verify SQLite has the chunk.
	count, _ := db.CountContextChunks("s-test")
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestRollingStoreTotalTokens(t *testing.T) {
	rs, _ := openTestStore(t)

	content := "chunk content with some words for token estimation"
	for i := 0; i < 5; i++ {
		_ = rs.Append(&Chunk{Kind: "event", Content: content})
	}

	total := rs.TotalTokens()
	if total == 0 {
		t.Error("expected non-zero total tokens")
	}
}

func TestRollingStoreJSONL(t *testing.T) {
	rs, _ := openTestStore(t)

	// Append 5 chunks.
	for i := 0; i < 5; i++ {
		_ = rs.Append(&Chunk{
			Kind:    "event",
			Content: fmt.Sprintf("chunk %d content", i),
		})
	}
	rs.Close() // flush

	// Read JSONL file and verify.
	f, err := os.Open(rs.cfg.JSONLPath)
	if err != nil {
		t.Fatalf("open jsonl: %v", err)
	}
	defer f.Close()

	var lines []Chunk
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var c Chunk
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			t.Errorf("bad jsonl line: %v", err)
			continue
		}
		lines = append(lines, c)
	}

	if len(lines) != 5 {
		t.Errorf("jsonl lines = %d, want 5", len(lines))
	}
}

func TestRollingStoreEviction(t *testing.T) {
	rs, db := openTestStore(t)

	// MaxTokens = 1000. Append 100-token chunks until over budget.
	longContent := make([]byte, 400) // ~100 tokens (400 chars / 4)
	for i := range longContent {
		longContent[i] = 'a'
	}

	// Append 15 chunks (15 * 100 = 1500 tokens, over 1000 budget).
	for i := 0; i < 15; i++ {
		if err := rs.Append(&Chunk{Kind: "event", Content: string(longContent)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Total should be ≤ 90% of 1000 = 900 after eviction.
	total, _ := db.TotalContextTokens("s-test")
	if total > 900 {
		t.Errorf("post-evict total = %d, want ≤ 900", total)
	}
}

func TestRollingStoreRetrieveByKeyword(t *testing.T) {
	rs, _ := openTestStore(t)

	_ = rs.Append(&Chunk{Kind: "task_result", Content: "analyzed the authentication flow"})
	_ = rs.Append(&Chunk{Kind: "task_result", Content: "implemented the login endpoint"})
	_ = rs.Append(&Chunk{Kind: "task_result", Content: "reviewed the security policies"})

	// Query for "authentication".
	chunks, err := rs.Retrieve("authentication", time.Now())
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	// Top result should contain "authentication".
	if chunks[0].Content != "analyzed the authentication flow" {
		t.Errorf("top result: %q", chunks[0].Content)
	}
}

func TestRollingStoreRetrieveRecency(t *testing.T) {
	rs, _ := openTestStore(t)

	// Append 5 chunks with increasing timestamps.
	base := time.Now().Add(-5 * time.Hour)
	for i := 0; i < 5; i++ {
		rs.Append(&Chunk{
			Kind:      "event",
			Content:   fmt.Sprintf("generic content %d", i),
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
		})
	}

	// Query with no keywords — should return recent first.
	chunks, err := rs.Retrieve("", time.Now())
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	// Most recent first.
	if chunks[0].Content != "generic content 4" {
		t.Errorf("first = %q, want 'generic content 4'", chunks[0].Content)
	}
}

func TestRollingStoreRetrieveEmpty(t *testing.T) {
	rs, _ := openTestStore(t)

	chunks, err := rs.Retrieve("anything", time.Now())
	if err != nil {
		t.Fatalf("retrieve empty: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestRollingStoreRetrieveBudget(t *testing.T) {
	rs, _ := openTestStore(t)
	rs.cfg.RetrievalBudget = 100 // tight budget

	// Append 10 chunks with ~50 tokens each.
	content := make([]byte, 200) // 50 tokens
	for i := range content {
		content[i] = 'x'
	}
	for i := 0; i < 10; i++ {
		rs.Append(&Chunk{Kind: "event", Content: string(content)})
	}

	// Budget = 100 tokens. Should get at most 2 chunks (2 * 50 = 100).
	chunks, err := rs.Retrieve("", time.Now())
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(chunks) > 2 {
		t.Errorf("chunk count = %d, want ≤ 2 (budget)", len(chunks))
	}
}

func TestRollingStoreRenderRelevant(t *testing.T) {
	rs, _ := openTestStore(t)

	// Empty store.
	if s := rs.RenderRelevant("anything"); s != "" {
		t.Errorf("empty render = %q, want empty", s)
	}

	rs.Append(&Chunk{
		TaskID:  "t-1",
		Agent:   "claude",
		Kind:    "task_result",
		Content: "found three issues in the auth module",
	})

	rendered := rs.RenderRelevant("auth")
	if rendered == "" {
		t.Fatal("expected non-empty render")
	}
	// Check format.
	if !contains(rendered, "=== m3m0ry:") {
		t.Error("missing header")
	}
	if !contains(rendered, "=== end m3m0ry ===") {
		t.Error("missing footer")
	}
	if !contains(rendered, "found three issues") {
		t.Error("missing content")
	}
}

func TestRollingStoreStats(t *testing.T) {
	rs, _ := openTestStore(t)

	rs.Append(&Chunk{Kind: "event", Content: "first chunk"})
	rs.Append(&Chunk{Kind: "event", Content: "second chunk"})

	stats, err := rs.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.ChunkCount != 2 {
		t.Errorf("count = %d, want 2", stats.ChunkCount)
	}
	if stats.TotalTokens == 0 {
		t.Error("expected non-zero tokens")
	}
	if stats.MaxSeq != 2 {
		t.Errorf("maxSeq = %d, want 2", stats.MaxSeq)
	}
	if stats.JSONLPath == "" {
		t.Error("expected jsonl path")
	}
}

func TestRollingStoreConcurrentAppend(t *testing.T) {
	rs, db := openTestStore(t)

	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				rs.Append(&Chunk{
					Kind:    "event",
					Content: fmt.Sprintf("g=%d i=%d", g, i),
				})
			}
		}(g)
	}
	wg.Wait()

	count, _ := db.CountContextChunks("s-test")
	if count != 50 {
		t.Errorf("count = %d, want 50", count)
	}
}

func TestRollingStoreClampLargeContent(t *testing.T) {
	rs, _ := openTestStore(t)
	rs.cfg.MaxChunkTokens = 10 // tiny

	// Append 100-byte chunk (25 tokens) — should be clamped.
	rs.Append(&Chunk{Kind: "event", Content: "This is a reasonably long content that exceeds the tiny max chunk"})

	// Verify by reading back.
	chunks, _ := rs.Retrieve("", time.Now())
	if len(chunks) == 0 {
		t.Fatal("expected chunk")
	}
	if len(chunks[0].Content) > 10*4+30 { // 10 tokens * 4 chars + TRUNCATED marker
		t.Errorf("content not clamped: %d chars", len(chunks[0].Content))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
