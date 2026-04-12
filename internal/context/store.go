package context

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rohanrgit/ag3nts/internal/logging"
	"github.com/rohanrgit/ag3nts/internal/store"
)

// Config controls the rolling context window behavior.
type Config struct {
	Enabled         bool
	MaxTokens       int     // default 10_000_000
	MaxChunkTokens  int     // truncate individual chunks larger than this (default 4_000)
	JSONLPath       string  // full path to append-only log file
	EvictHeadroom   float64 // fraction of MaxTokens to reserve after eviction (default 0.10 -> 90%)
	RetrievalLimit  int     // max chunks returned per retrieve call (default 40)
	RetrievalBudget int     // max total tokens in a retrieval (default 50_000)
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:         true,
		MaxTokens:       10_000_000,
		MaxChunkTokens:  4000,
		EvictHeadroom:   0.10,
		RetrievalLimit:  40,
		RetrievalBudget: 50_000,
	}
}

// RollingStore is a dual-backed (SQLite + JSONL) token-bounded append log
// with keyword + recency retrieval.
type RollingStore struct {
	cfg    Config
	db     *store.DB
	sessID string
	jsonl  *os.File
	logger *logging.Logger

	jsonlMu sync.Mutex // serializes JSONL writes
	writeMu sync.Mutex // serializes Append + maybeEvict

	totalTokens int64 // atomic running total (read under writeMu when making eviction decisions)
	seq         int64 // atomic monotonic counter
	appendCount int64 // atomic; Sync() every 16 appends
}

// Open creates or opens a rolling store backed by SQLite (via db) and a
// JSONL file at cfg.JSONLPath. The directory is created if needed.
// sessID is the current orchestrator session — all chunks belong to it.
func Open(cfg Config, db *store.DB, sessID string, logger *logging.Logger) (*RollingStore, error) {
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 10_000_000
	}
	if cfg.MaxChunkTokens <= 0 {
		cfg.MaxChunkTokens = 4000
	}
	if cfg.EvictHeadroom <= 0 || cfg.EvictHeadroom >= 1 {
		cfg.EvictHeadroom = 0.10
	}
	if cfg.RetrievalLimit <= 0 {
		cfg.RetrievalLimit = 40
	}
	if cfg.RetrievalBudget <= 0 {
		cfg.RetrievalBudget = 50_000
	}
	if cfg.JSONLPath == "" {
		return nil, fmt.Errorf("JSONLPath is required")
	}
	if db == nil {
		return nil, fmt.Errorf("db is required")
	}
	if sessID == "" {
		return nil, fmt.Errorf("sessID is required")
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(cfg.JSONLPath), 0700); err != nil {
		return nil, fmt.Errorf("create jsonl dir: %w", err)
	}

	// Open JSONL file (append-only).
	f, err := os.OpenFile(cfg.JSONLPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open jsonl: %w", err)
	}

	rs := &RollingStore{
		cfg:    cfg,
		db:     db,
		sessID: sessID,
		jsonl:  f,
		logger: logger,
	}

	// Initialize totalTokens and seq from SQLite state.
	if total, err := db.TotalContextTokens(sessID); err == nil {
		atomic.StoreInt64(&rs.totalTokens, int64(total))
	}
	if maxSeq, err := db.MaxContextSeq(sessID); err == nil {
		atomic.StoreInt64(&rs.seq, maxSeq)
	}

	return rs, nil
}

// Close flushes and closes the JSONL file.
func (r *RollingStore) Close() error {
	r.jsonlMu.Lock()
	defer r.jsonlMu.Unlock()
	if r.jsonl != nil {
		_ = r.jsonl.Sync()
		err := r.jsonl.Close()
		r.jsonl = nil
		return err
	}
	return nil
}

// TotalTokens returns the current total token count (fast, atomic read).
func (r *RollingStore) TotalTokens() int64 {
	return atomic.LoadInt64(&r.totalTokens)
}

// Stats returns a snapshot of the current store state.
func (r *RollingStore) Stats() (*Stats, error) {
	count, err := r.db.CountContextChunks(r.sessID)
	if err != nil {
		return nil, err
	}
	total := int(atomic.LoadInt64(&r.totalTokens))
	maxSeq := atomic.LoadInt64(&r.seq)

	stats := &Stats{
		TotalTokens: total,
		ChunkCount:  count,
		MaxSeq:      maxSeq,
		JSONLPath:   r.cfg.JSONLPath,
	}
	if info, err := os.Stat(r.cfg.JSONLPath); err == nil {
		stats.JSONLBytes = info.Size()
	}
	return stats, nil
}

// Append writes a chunk to SQLite, the JSONL file, and updates running totals.
// Triggers eviction if total tokens exceed MaxTokens.
func (r *RollingStore) Append(chunk *Chunk) error {
	if chunk == nil {
		return fmt.Errorf("chunk is nil")
	}
	if !r.cfg.Enabled {
		return nil
	}

	// Fill in missing fields.
	if chunk.SessionID == "" {
		chunk.SessionID = r.sessID
	}
	if chunk.CreatedAt.IsZero() {
		chunk.CreatedAt = time.Now().UTC()
	}
	chunk.Seq = atomic.AddInt64(&r.seq, 1)

	// Clamp content to MaxChunkTokens (approx — 4 chars per token).
	maxChars := r.cfg.MaxChunkTokens * 4
	if len(chunk.Content) > maxChars {
		chunk.Content = chunk.Content[:maxChars] + "\n[TRUNCATED]"
	}

	// Estimate tokens and extract keywords if not already set.
	if chunk.TokenCount == 0 {
		chunk.TokenCount = estimateTokens(chunk.Content)
	}
	if len(chunk.Keywords) == 0 {
		chunk.Keywords = extractKeywords(chunk.Content)
	}

	// Single-writer invariant: writeMu serializes Append + maybeEvict.
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	// Insert into SQLite.
	rec := &store.ContextChunkRecord{
		SessionID:  chunk.SessionID,
		TaskID:     chunk.TaskID,
		Agent:      chunk.Agent,
		Kind:       chunk.Kind,
		Content:    chunk.Content,
		TokenCount: chunk.TokenCount,
		Keywords:   strings.Join(chunk.Keywords, " "),
		Seq:        chunk.Seq,
		CreatedAt:  chunk.CreatedAt,
	}
	id, err := r.db.InsertContextChunk(rec)
	if err != nil {
		return fmt.Errorf("insert chunk: %w", err)
	}
	chunk.ID = id
	atomic.AddInt64(&r.totalTokens, int64(chunk.TokenCount))

	// Append to JSONL (serialized separately from writeMu for safety).
	if err := r.appendJSONL(chunk); err != nil {
		// Logged but non-fatal — SQLite is authoritative.
		if r.logger != nil {
			r.logger.Errorf("m3m0ry", "jsonl append failed: %v", err)
		}
	}

	// Evict if over budget.
	if atomic.LoadInt64(&r.totalTokens) > int64(r.cfg.MaxTokens) {
		r.maybeEvict()
	}

	return nil
}

// maybeEvict is called under writeMu when totalTokens > MaxTokens.
// Evicts oldest chunks to bring total down to (1 - EvictHeadroom) * MaxTokens.
func (r *RollingStore) maybeEvict() {
	target := int(float64(r.cfg.MaxTokens) * (1 - r.cfg.EvictHeadroom))
	evicted, err := r.db.EvictOldestContextChunks(r.sessID, target)
	if err != nil {
		if r.logger != nil {
			r.logger.Errorf("m3m0ry", "evict failed: %v", err)
		}
		return
	}

	// Refresh totalTokens from SQLite.
	if total, err := r.db.TotalContextTokens(r.sessID); err == nil {
		atomic.StoreInt64(&r.totalTokens, int64(total))
	}

	if r.logger != nil && evicted > 0 {
		r.logger.Infof("m3m0ry", "evicted %d chunks, total now %d tokens", evicted, atomic.LoadInt64(&r.totalTokens))
	}
}

// appendJSONL writes one JSON line under jsonlMu. Explicit Sync() every 16 writes.
func (r *RollingStore) appendJSONL(chunk *Chunk) error {
	data, err := json.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("marshal chunk: %w", err)
	}

	r.jsonlMu.Lock()
	defer r.jsonlMu.Unlock()

	if r.jsonl == nil {
		return fmt.Errorf("jsonl file is closed")
	}

	if _, err := r.jsonl.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write jsonl: %w", err)
	}

	// Sync every 16 appends for crash safety without killing performance.
	n := atomic.AddInt64(&r.appendCount, 1)
	if n%16 == 0 {
		_ = r.jsonl.Sync()
	}

	return nil
}

// Retrieve returns up to RetrievalLimit chunks matching the query,
// ranked by keyword hits + recency. Total tokens capped at RetrievalBudget.
// If query is empty, returns most recent chunks.
func (r *RollingStore) Retrieve(query string, now time.Time) ([]*Chunk, error) {
	if now.IsZero() {
		now = time.Now()
	}
	keywords := extractKeywords(query)

	// Fetch 3x the limit so we can re-rank.
	fetchLimit := r.cfg.RetrievalLimit * 3
	records, err := r.db.QueryContextChunks(r.sessID, keywords, fetchLimit)
	if err != nil {
		return nil, fmt.Errorf("query chunks: %w", err)
	}

	// Convert to Chunk and compute scores.
	type scored struct {
		chunk *Chunk
		score float64
	}
	scoredChunks := make([]scored, 0, len(records))
	for _, rec := range records {
		kw := splitKeywords(rec.Keywords)
		chunk := &Chunk{
			ID:         rec.ID,
			SessionID:  rec.SessionID,
			TaskID:     rec.TaskID,
			Agent:      rec.Agent,
			Kind:       rec.Kind,
			Content:    rec.Content,
			TokenCount: rec.TokenCount,
			Keywords:   kw,
			Seq:        rec.Seq,
			CreatedAt:  rec.CreatedAt,
		}

		// Score = keywordHits * 10 + recencyBonus
		hits := 0
		kwSet := make(map[string]bool, len(kw))
		for _, k := range kw {
			kwSet[k] = true
		}
		for _, q := range keywords {
			if kwSet[q] {
				hits++
			}
		}
		hoursSince := now.Sub(chunk.CreatedAt).Hours()
		recency := 10.0 - hoursSince
		if recency < 0 {
			recency = 0
		}
		score := float64(hits)*10.0 + recency

		scoredChunks = append(scoredChunks, scored{chunk: chunk, score: score})
	}

	// Sort by score DESC; ties broken by recency (newer first).
	sort.SliceStable(scoredChunks, func(i, j int) bool {
		if scoredChunks[i].score != scoredChunks[j].score {
			return scoredChunks[i].score > scoredChunks[j].score
		}
		return scoredChunks[i].chunk.CreatedAt.After(scoredChunks[j].chunk.CreatedAt)
	})

	// Apply limit + token budget.
	var out []*Chunk
	budget := r.cfg.RetrievalBudget
	used := 0
	for _, sc := range scoredChunks {
		if len(out) >= r.cfg.RetrievalLimit {
			break
		}
		if used+sc.chunk.TokenCount > budget {
			break
		}
		out = append(out, sc.chunk)
		used += sc.chunk.TokenCount
	}
	return out, nil
}
