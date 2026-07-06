package store

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestContextChunkInsert(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-ctx", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	id, err := db.InsertContextChunk(&ContextChunkRecord{
		SessionID:  "s-ctx",
		TaskID:     "t-1",
		Agent:      "claude",
		Kind:       "task_result",
		Content:    "analyzed the codebase structure",
		TokenCount: 100,
		Keywords:   "analyzed codebase structure",
		Seq:        1,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero ID")
	}
}

func TestContextChunkTotalTokens(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-tot", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Insert 5 chunks with varying token counts.
	for i := 0; i < 5; i++ {
		_, err := db.InsertContextChunk(&ContextChunkRecord{
			SessionID:  "s-tot",
			Kind:       "task_result",
			Content:    fmt.Sprintf("chunk %d", i),
			TokenCount: (i + 1) * 100, // 100, 200, 300, 400, 500
			Seq:        int64(i + 1),
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	total, err := db.TotalContextTokens("s-tot")
	if err != nil {
		t.Fatalf("total: %v", err)
	}
	if total != 1500 {
		t.Errorf("total = %d, want 1500", total)
	}
}

func TestContextChunkTotalTokensEmpty(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-empty", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	total, err := db.TotalContextTokens("s-empty")
	if err != nil {
		t.Fatalf("total empty: %v", err)
	}
	if total != 0 {
		t.Errorf("total empty = %d, want 0", total)
	}
}

func TestContextChunkEviction(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-evict", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Insert 10 chunks with 100 tokens each = 1000 total.
	for i := 0; i < 10; i++ {
		_, err := db.InsertContextChunk(&ContextChunkRecord{
			SessionID:  "s-evict",
			Kind:       "task_result",
			Content:    fmt.Sprintf("chunk %d", i),
			TokenCount: 100,
			Seq:        int64(i + 1),
			CreatedAt:  time.Now().Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// Evict to 500 tokens (should drop 5 oldest chunks).
	evicted, err := db.EvictOldestContextChunks("s-evict", 500)
	if err != nil {
		t.Fatalf("evict: %v", err)
	}
	if evicted != 5 {
		t.Errorf("evicted = %d, want 5", evicted)
	}

	// Verify total is now 500.
	total, _ := db.TotalContextTokens("s-evict")
	if total != 500 {
		t.Errorf("total after evict = %d, want 500", total)
	}

	// Verify count.
	count, _ := db.CountContextChunks("s-evict")
	if count != 5 {
		t.Errorf("count after evict = %d, want 5", count)
	}
}

func TestContextChunkEvictionUnderTarget(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-under", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Insert 200 tokens total.
	_, _ = db.InsertContextChunk(&ContextChunkRecord{SessionID: "s-under", TokenCount: 100, Seq: 1})
	_, _ = db.InsertContextChunk(&ContextChunkRecord{SessionID: "s-under", TokenCount: 100, Seq: 2})

	// Try to evict with target 500 — should be no-op.
	evicted, err := db.EvictOldestContextChunks("s-under", 500)
	if err != nil {
		t.Fatalf("evict: %v", err)
	}
	if evicted != 0 {
		t.Errorf("evicted = %d, want 0 (already under target)", evicted)
	}
}

func TestContextChunkQueryByKeywords(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-query", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Insert chunks with different keywords.
	_, _ = db.InsertContextChunk(&ContextChunkRecord{
		SessionID: "s-query", Kind: "task_result",
		Content: "analyzed the codebase", Keywords: "analyzed codebase structure", Seq: 1,
	})
	_, _ = db.InsertContextChunk(&ContextChunkRecord{
		SessionID: "s-query", Kind: "task_result",
		Content: "wrote unit tests", Keywords: "wrote unit tests golang", Seq: 2,
	})
	_, _ = db.InsertContextChunk(&ContextChunkRecord{
		SessionID: "s-query", Kind: "task_result",
		Content: "fixed security issue", Keywords: "fixed security vulnerability", Seq: 3,
	})

	// Query by keyword "codebase".
	results, err := db.QueryContextChunks("s-query", []string{"codebase"}, 10)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("len = %d, want 1", len(results))
	}
	if len(results) > 0 && results[0].Content != "analyzed the codebase" {
		t.Errorf("unexpected result: %s", results[0].Content)
	}

	// Query by multiple keywords.
	results, _ = db.QueryContextChunks("s-query", []string{"tests", "security"}, 10)
	if len(results) != 2 {
		t.Errorf("len for multi-kw = %d, want 2", len(results))
	}
}

func TestContextChunkQueryEmptyKeywords(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-rec", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Insert 5 chunks.
	for i := 0; i < 5; i++ {
		_, _ = db.InsertContextChunk(&ContextChunkRecord{
			SessionID: "s-rec", Kind: "task_result",
			Content: fmt.Sprintf("chunk %d", i), Seq: int64(i + 1),
			CreatedAt: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	// Empty keywords = pure recency, limit 3.
	results, err := db.QueryContextChunks("s-rec", nil, 3)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("len = %d, want 3", len(results))
	}
	// Most recent first (Seq 5, 4, 3).
	if len(results) == 3 && results[0].Seq != 5 {
		t.Errorf("first.Seq = %d, want 5", results[0].Seq)
	}
}

func TestContextChunkListBySeq(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-list", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		_, _ = db.InsertContextChunk(&ContextChunkRecord{
			SessionID: "s-list", Kind: "event",
			Content: fmt.Sprintf("event %d", i), Seq: int64(i + 1),
		})
	}

	// List chunks after seq 5.
	results, err := db.ListContextChunks("s-list", 5, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("len = %d, want 5", len(results))
	}
	// Ordered ASC starting from seq 6.
	if len(results) == 5 && results[0].Seq != 6 {
		t.Errorf("first.Seq = %d, want 6", results[0].Seq)
	}
}

func TestContextChunkMaxSeq(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-max", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Empty session.
	maxSeq, err := db.MaxContextSeq("s-max")
	if err != nil {
		t.Fatalf("max empty: %v", err)
	}
	if maxSeq != 0 {
		t.Errorf("max empty = %d, want 0", maxSeq)
	}

	// With chunks.
	_, _ = db.InsertContextChunk(&ContextChunkRecord{SessionID: "s-max", Seq: 1})
	_, _ = db.InsertContextChunk(&ContextChunkRecord{SessionID: "s-max", Seq: 5})
	_, _ = db.InsertContextChunk(&ContextChunkRecord{SessionID: "s-max", Seq: 3})

	maxSeq, _ = db.MaxContextSeq("s-max")
	if maxSeq != 5 {
		t.Errorf("max = %d, want 5", maxSeq)
	}
}

func TestContextChunkConcurrentInsert(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-conc", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// 20 goroutines × 10 inserts each.
	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_, err := db.InsertContextChunk(&ContextChunkRecord{
					SessionID:  "s-conc",
					Kind:       "event",
					Content:    fmt.Sprintf("g=%d i=%d", g, i),
					TokenCount: 10,
					Seq:        int64(g*10 + i + 1),
				})
				if err != nil {
					t.Errorf("concurrent insert: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	count, _ := db.CountContextChunks("s-conc")
	if count != 200 {
		t.Errorf("count = %d, want 200", count)
	}

	total, _ := db.TotalContextTokens("s-conc")
	if total != 2000 {
		t.Errorf("total = %d, want 2000", total)
	}
}

func TestContextChunkSchemaV5Migration(t *testing.T) {
	// Verify that the schema version is current after migrate.
	db := openTestDB(t)
	var version int
	if err := db.db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 7 {
		t.Errorf("schema version = %d, want 7", version)
	}

	// Verify the context_chunks table exists.
	var count int
	err := db.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='context_chunks'`).Scan(&count)
	if err != nil || count != 1 {
		t.Errorf("context_chunks table not created: count=%d err=%v", count, err)
	}

	// Verify the FTS5 virtual table exists.
	err = db.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='context_chunks_fts'`).Scan(&count)
	if err != nil || count != 1 {
		t.Errorf("context_chunks_fts table not created: count=%d err=%v", count, err)
	}

	// Verify FTS5 works: insert a chunk and query via MATCH.
	_, err = db.InsertContextChunk(&ContextChunkRecord{
		SessionID:  "fts-test",
		Content:    "golang concurrency patterns with goroutines and channels",
		Keywords:   "golang concurrency patterns goroutines channels",
		TokenCount: 10,
		Seq:        1,
	})
	if err != nil {
		t.Fatalf("insert chunk: %v", err)
	}

	// FTS5 MATCH query should find it.
	chunks, err := db.QueryContextChunks("fts-test", []string{"golang"}, 10)
	if err != nil {
		t.Fatalf("FTS5 query: %v", err)
	}
	if len(chunks) != 1 {
		t.Errorf("expected 1 FTS5 match, got %d", len(chunks))
	}

	// Query for non-matching keyword should return empty.
	chunks, err = db.QueryContextChunks("fts-test", []string{"python"}, 10)
	if err != nil {
		t.Fatalf("FTS5 query: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 FTS5 matches, got %d", len(chunks))
	}
}

func TestContextChunkFTS5Eviction(t *testing.T) {
	// Verify that FTS5 index stays in sync when chunks are evicted.
	db := openTestDB(t)

	for i := 0; i < 5; i++ {
		_, err := db.InsertContextChunk(&ContextChunkRecord{
			SessionID:  "evict-fts",
			Content:    fmt.Sprintf("document number %d about testing", i),
			Keywords:   fmt.Sprintf("document number testing chunk%d", i),
			TokenCount: 100,
			Seq:        int64(i + 1),
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// All 5 should be findable via FTS.
	chunks, _ := db.QueryContextChunks("evict-fts", []string{"testing"}, 10)
	if len(chunks) != 5 {
		t.Fatalf("before eviction: expected 5 FTS matches, got %d", len(chunks))
	}

	// Evict to 200 tokens (should remove 3 oldest chunks: 500 - 200 = 300, need 3*100).
	evicted, err := db.EvictOldestContextChunks("evict-fts", 200)
	if err != nil {
		t.Fatalf("evict: %v", err)
	}
	if evicted != 3 {
		t.Errorf("evicted %d, want 3", evicted)
	}

	// Only 2 remaining should be findable via FTS.
	chunks, _ = db.QueryContextChunks("evict-fts", []string{"testing"}, 10)
	if len(chunks) != 2 {
		t.Errorf("after eviction: expected 2 FTS matches, got %d", len(chunks))
	}
}
