package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Memory represents a single knowledge entry in the cross-agent memory store.
type Memory struct {
	ID        string
	Category  string // "global", "project", "session"
	Scope     string // project path or session ID (empty for global)
	Key       string // topic/category key (e.g. "architecture", "conventions")
	Content   string
	Source    string // which agent created this
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MemoryStore provides cross-agent knowledge persistence.
type MemoryStore struct {
	db *DB
}

// NewMemoryStore creates a memory store backed by the given database.
func NewMemoryStore(db *DB) *MemoryStore {
	return &MemoryStore{db: db}
}

// Store inserts or updates a memory entry. If a memory with the same
// category+scope+key exists, it is updated.
func (m *MemoryStore) Store(mem *Memory) error {
	now := time.Now().UTC().Format(time.RFC3339)

	if mem.ID == "" {
		mem.ID = fmt.Sprintf("mem_%d", time.Now().UnixNano())
	}

	// Upsert: try update first, insert if no rows affected.
	result, err := m.db.db.Exec(`
		UPDATE memories SET content = ?, source = ?, updated_at = ?
		WHERE category = ? AND scope = ? AND key = ?`,
		mem.Content, mem.Source, now,
		mem.Category, mem.Scope, mem.Key,
	)
	if err != nil {
		return fmt.Errorf("update memory: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows > 0 {
		return nil // updated existing
	}

	// Insert new.
	_, err = m.db.db.Exec(`
		INSERT INTO memories (id, category, scope, key, content, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		mem.ID, mem.Category, mem.Scope, mem.Key, mem.Content, mem.Source, now, now,
	)
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	return nil
}

// Query returns memories matching the given category and scope.
// If keywords is non-empty, filters to memories whose key or content
// contains at least one keyword (case-insensitive).
func (m *MemoryStore) Query(category, scope string, keywords []string) ([]*Memory, error) {
	query := `SELECT id, category, scope, key, content, source, created_at, updated_at
		FROM memories WHERE category = ? AND scope = ?`
	args := []any{category, scope}

	if len(keywords) > 0 {
		var clauses []string
		for _, kw := range keywords {
			clauses = append(clauses, "(LOWER(key) LIKE ? OR LOWER(content) LIKE ?)")
			pattern := "%" + strings.ToLower(kw) + "%"
			args = append(args, pattern, pattern)
		}
		query += " AND (" + strings.Join(clauses, " OR ") + ")"
	}

	query += " ORDER BY updated_at DESC"

	rows, err := m.db.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows)
}

// GetAll returns all memories for a category and scope.
func (m *MemoryStore) GetAll(category, scope string) ([]*Memory, error) {
	return m.Query(category, scope, nil)
}

// Get returns a single memory by category+scope+key.
func (m *MemoryStore) Get(category, scope, key string) (*Memory, error) {
	row := m.db.db.QueryRow(`
		SELECT id, category, scope, key, content, source, created_at, updated_at
		FROM memories WHERE category = ? AND scope = ? AND key = ?`,
		category, scope, key,
	)

	var mem Memory
	var createdStr, updatedStr string
	err := row.Scan(&mem.ID, &mem.Category, &mem.Scope, &mem.Key,
		&mem.Content, &mem.Source, &createdStr, &updatedStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get memory: %w", err)
	}
	mem.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	mem.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return &mem, nil
}

// Delete removes a memory by category+scope+key.
func (m *MemoryStore) Delete(category, scope, key string) error {
	_, err := m.db.db.Exec(`DELETE FROM memories WHERE category = ? AND scope = ? AND key = ?`,
		category, scope, key)
	return err
}

// InjectRelevant builds a context string from memories relevant to a task.
// Queries both global and scope-specific memories, optionally filtered
// by keywords extracted from the task description.
func (m *MemoryStore) InjectRelevant(scope, taskDescription string) string {
	// Extract simple keywords from the task description.
	keywords := extractKeywords(taskDescription)

	var sections []string

	// Global memories.
	globals, err := m.Query("global", "", keywords)
	if err == nil && len(globals) > 0 {
		var lines []string
		for _, mem := range globals {
			lines = append(lines, fmt.Sprintf("[%s] %s", mem.Key, truncate(mem.Content, 500)))
		}
		sections = append(sections, "=== Global Memory ===\n"+strings.Join(lines, "\n"))
	}

	// Project-scoped memories.
	if scope != "" {
		projectMems, err := m.Query("project", scope, keywords)
		if err == nil && len(projectMems) > 0 {
			var lines []string
			for _, mem := range projectMems {
				lines = append(lines, fmt.Sprintf("[%s] %s", mem.Key, truncate(mem.Content, 500)))
			}
			sections = append(sections, "=== Project Memory ===\n"+strings.Join(lines, "\n"))
		}
	}

	if len(sections) == 0 {
		return ""
	}
	return strings.Join(sections, "\n\n")
}

// extractKeywords pulls simple keywords from text for memory lookup.
// Returns the longest words (likely to be meaningful) up to 5.
func extractKeywords(text string) []string {
	if text == "" {
		return nil
	}

	words := strings.Fields(strings.ToLower(text))
	// Filter to words >= 4 chars (skip articles, prepositions).
	var meaningful []string
	seen := make(map[string]bool)
	for _, w := range words {
		// Strip punctuation.
		w = strings.Trim(w, ".,;:!?\"'()[]{}—-")
		if len(w) < 4 || seen[w] {
			continue
		}
		seen[w] = true
		meaningful = append(meaningful, w)
	}

	// Take at most 5 keywords.
	if len(meaningful) > 5 {
		meaningful = meaningful[:5]
	}
	return meaningful
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func scanMemories(rows *sql.Rows) ([]*Memory, error) {
	var memories []*Memory
	for rows.Next() {
		var mem Memory
		var createdStr, updatedStr string
		if err := rows.Scan(&mem.ID, &mem.Category, &mem.Scope, &mem.Key,
			&mem.Content, &mem.Source, &createdStr, &updatedStr); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		mem.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		mem.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
		memories = append(memories, &mem)
	}
	return memories, rows.Err()
}
