package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ContextChunkRecord maps to the context_chunks table — raw rolling session history.
type ContextChunkRecord struct {
	ID         int64
	SessionID  string
	TaskID     string
	Agent      string
	Kind       string // "task_result", "event_message", "event_tool_use", etc.
	Content    string
	TokenCount int
	Keywords   string // space-delimited lowercase
	Seq        int64
	CreatedAt  time.Time
}

// InsertContextChunk inserts a new chunk and returns its ID.
func (d *DB) InsertContextChunk(rec *ContextChunkRecord) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	} else {
		now = rec.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := d.db.Exec(`
		INSERT INTO context_chunks (session_id, task_id, agent, kind, content, token_count, keywords, seq, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.SessionID, rec.TaskID, rec.Agent, rec.Kind, rec.Content,
		rec.TokenCount, rec.Keywords, rec.Seq, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert context chunk: %w", err)
	}
	return result.LastInsertId()
}

// TotalContextTokens returns the sum of token_count for all chunks in a session.
func (d *DB) TotalContextTokens(sessionID string) (int, error) {
	var total sql.NullInt64
	err := d.db.QueryRow(`SELECT SUM(token_count) FROM context_chunks WHERE session_id = ?`, sessionID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("total context tokens: %w", err)
	}
	if !total.Valid {
		return 0, nil
	}
	return int(total.Int64), nil
}

// EvictOldestContextChunks deletes oldest chunks from a session until
// total tokens drop to targetTokens or below. Returns number evicted.
//
// Strategy: walk chunks oldest-first, accumulating tokens until we've
// identified exactly the set needed to free, then delete that set in
// one statement. This is precise (no over-eviction) and fast (two queries).
func (d *DB) EvictOldestContextChunks(sessionID string, targetTokens int) (int, error) {
	total, err := d.TotalContextTokens(sessionID)
	if err != nil {
		return 0, err
	}
	if total <= targetTokens {
		return 0, nil
	}
	excess := total - targetTokens

	// Walk oldest chunks, collect IDs until we've freed enough tokens.
	rows, err := d.db.Query(`
		SELECT id, token_count FROM context_chunks
		WHERE session_id = ?
		ORDER BY created_at ASC, id ASC`, sessionID)
	if err != nil {
		return 0, fmt.Errorf("scan oldest chunks: %w", err)
	}
	defer rows.Close()

	var idsToDelete []int64
	freed := 0
	for rows.Next() {
		var id int64
		var tokens int
		if err := rows.Scan(&id, &tokens); err != nil {
			return 0, fmt.Errorf("scan eviction row: %w", err)
		}
		idsToDelete = append(idsToDelete, id)
		freed += tokens
		if freed >= excess {
			break
		}
	}
	rows.Close()

	if len(idsToDelete) == 0 {
		return 0, nil
	}

	// Delete the identified IDs in a single statement.
	placeholders := strings.Repeat("?,", len(idsToDelete))
	placeholders = placeholders[:len(placeholders)-1] // trim trailing comma
	args := make([]any, len(idsToDelete))
	for i, id := range idsToDelete {
		args[i] = id
	}
	_, err = d.db.Exec(
		`DELETE FROM context_chunks WHERE id IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return 0, fmt.Errorf("delete evicted chunks: %w", err)
	}

	return len(idsToDelete), nil
}

// QueryContextChunks returns chunks matching any of the given keywords,
// ordered by FTS5 relevance (bm25), limited to the specified count.
// Uses FTS5 MATCH for fast full-text search when keywords are provided.
//
// If keywords is empty, returns the most recent chunks (pure recency).
func (d *DB) QueryContextChunks(sessionID string, keywords []string, limit int) ([]*ContextChunkRecord, error) {
	if limit <= 0 {
		limit = 40
	}

	if len(keywords) == 0 {
		// Pure recency fallback — no FTS needed.
		rows, err := d.db.Query(`
			SELECT id, session_id, task_id, agent, kind, content, token_count, keywords, seq, created_at
			FROM context_chunks
			WHERE session_id = ?
			ORDER BY created_at DESC
			LIMIT ?`, sessionID, limit)
		if err != nil {
			return nil, fmt.Errorf("query context chunks (recency): %w", err)
		}
		defer rows.Close()
		return scanContextChunks(rows)
	}

	// Build FTS5 query: join keywords with OR for any-match semantics.
	// FTS5 syntax: "word1 OR word2 OR word3"
	var ftsTerms []string
	for _, kw := range keywords {
		// Escape quotes in keywords to prevent FTS5 syntax injection.
		escaped := strings.ReplaceAll(strings.ToLower(kw), `"`, `""`)
		ftsTerms = append(ftsTerms, `"`+escaped+`"`)
	}
	ftsQuery := strings.Join(ftsTerms, " OR ")

	// Join FTS results with the main table to filter by session_id and
	// get the full row data. Order by bm25 relevance (lower = better match).
	rows, err := d.db.Query(`
		SELECT c.id, c.session_id, c.task_id, c.agent, c.kind, c.content,
		       c.token_count, c.keywords, c.seq, c.created_at
		FROM context_chunks_fts f
		JOIN context_chunks c ON c.id = f.rowid
		WHERE context_chunks_fts MATCH ?
		  AND c.session_id = ?
		ORDER BY bm25(context_chunks_fts)
		LIMIT ?`, ftsQuery, sessionID, limit)
	if err != nil {
		// Fallback to LIKE if FTS5 fails (e.g., invalid query syntax).
		return d.queryContextChunksLike(sessionID, keywords, limit)
	}
	defer rows.Close()
	return scanContextChunks(rows)
}

// queryContextChunksLike is the legacy LIKE-based fallback used when
// FTS5 is unavailable or the query fails. Kept for robustness.
func (d *DB) queryContextChunksLike(sessionID string, keywords []string, limit int) ([]*ContextChunkRecord, error) {
	args := []any{sessionID}
	var clauses []string
	for _, kw := range keywords {
		clauses = append(clauses, "keywords LIKE ?")
		args = append(args, "%"+strings.ToLower(kw)+"%")
	}
	query := `SELECT id, session_id, task_id, agent, kind, content, token_count, keywords, seq, created_at
		FROM context_chunks
		WHERE session_id = ? AND (` + strings.Join(clauses, " OR ") + `)
		ORDER BY created_at DESC
		LIMIT ?`
	args = append(args, limit)

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query context chunks (like fallback): %w", err)
	}
	defer rows.Close()
	return scanContextChunks(rows)
}

// ListContextChunks returns chunks for a session with seq > afterSeq,
// ordered by seq ASC. Used for sequential iteration.
func (d *DB) ListContextChunks(sessionID string, afterSeq int64, limit int) ([]*ContextChunkRecord, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := d.db.Query(`
		SELECT id, session_id, task_id, agent, kind, content, token_count, keywords, seq, created_at
		FROM context_chunks
		WHERE session_id = ? AND seq > ?
		ORDER BY seq ASC
		LIMIT ?`, sessionID, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("list context chunks: %w", err)
	}
	defer rows.Close()

	return scanContextChunks(rows)
}

// CountContextChunks returns the number of chunks for a session.
func (d *DB) CountContextChunks(sessionID string) (int, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM context_chunks WHERE session_id = ?`, sessionID).Scan(&count)
	return count, err
}

// NextTaskResultAfterSeq returns the first task_result chunk in the
// given session with seq strictly greater than afterSeq. Used for
// turn pairing in m3m0ry retrieval — when a user_prompt chunk matches
// a query, this finds the corresponding assistant response so the
// pair is returned together. Returns nil if no such chunk exists.
func (d *DB) NextTaskResultAfterSeq(sessionID string, afterSeq int64) (*ContextChunkRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, session_id, task_id, agent, kind, content, token_count, keywords, seq, created_at
		FROM context_chunks
		WHERE session_id = ? AND kind = 'task_result' AND seq > ?
		ORDER BY seq ASC
		LIMIT 1`, sessionID, afterSeq)
	if err != nil {
		return nil, fmt.Errorf("next task_result: %w", err)
	}
	defer rows.Close()

	chunks, err := scanContextChunks(rows)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	return chunks[0], nil
}

// MaxContextSeq returns the highest seq number for a session (0 if empty).
func (d *DB) MaxContextSeq(sessionID string) (int64, error) {
	var maxSeq sql.NullInt64
	err := d.db.QueryRow(`SELECT MAX(seq) FROM context_chunks WHERE session_id = ?`, sessionID).Scan(&maxSeq)
	if err != nil {
		return 0, err
	}
	if !maxSeq.Valid {
		return 0, nil
	}
	return maxSeq.Int64, nil
}

func scanContextChunks(rows *sql.Rows) ([]*ContextChunkRecord, error) {
	var chunks []*ContextChunkRecord
	for rows.Next() {
		var rec ContextChunkRecord
		var createdStr string
		if err := rows.Scan(
			&rec.ID, &rec.SessionID, &rec.TaskID, &rec.Agent, &rec.Kind,
			&rec.Content, &rec.TokenCount, &rec.Keywords, &rec.Seq, &createdStr,
		); err != nil {
			return nil, fmt.Errorf("scan context chunk: %w", err)
		}
		rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		chunks = append(chunks, &rec)
	}
	return chunks, rows.Err()
}
