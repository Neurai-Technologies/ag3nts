package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SessionRecord maps to the sessions table.
type SessionRecord struct {
	ID                string
	Name              string
	WorkingDir        string
	PrimaryAgent      string
	Status            string // active, completed, failed
	CreatedAt         time.Time
	UpdatedAt         time.Time
	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalCachedTokens int64
	TotalCostUSD      float64
	ResumeIDs         map[string]string // agent_name → provider session ID (for cross-restart resume)
}

// CreateSession inserts a new session record.
func (d *DB) CreateSession(rec *SessionRecord) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = rec.CreatedAt
	}
	_, err := d.db.Exec(`
		INSERT INTO sessions (id, name, working_dir, primary_agent, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.Name, rec.WorkingDir, rec.PrimaryAgent, rec.Status,
		rec.CreatedAt.Format(time.RFC3339), now,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// GetSession retrieves a session by ID.
func (d *DB) GetSession(id string) (*SessionRecord, error) {
	row := d.db.QueryRow(`
		SELECT id, name, working_dir, primary_agent, status,
		       created_at, updated_at,
		       total_input_tokens, total_output_tokens, total_cached_tokens, total_cost_usd,
		       resume_ids
		FROM sessions WHERE id = ?`, id)

	var rec SessionRecord
	var createdStr, updatedStr, resumeJSON string
	err := row.Scan(
		&rec.ID, &rec.Name, &rec.WorkingDir, &rec.PrimaryAgent, &rec.Status,
		&createdStr, &updatedStr,
		&rec.TotalInputTokens, &rec.TotalOutputTokens, &rec.TotalCachedTokens, &rec.TotalCostUSD,
		&resumeJSON,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session %s: %w", id, err)
	}
	rec.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	rec.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	rec.ResumeIDs = make(map[string]string)
	_ = json.Unmarshal([]byte(resumeJSON), &rec.ResumeIDs)
	return &rec, nil
}

// UpdateSessionStatus sets the session's status and updated_at timestamp.
func (d *DB) UpdateSessionStatus(id, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.Exec(`UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?`, status, now, id)
	return err
}

// AddTokenUsage atomically adds token counts and cost to a session.
func (d *DB) AddTokenUsage(sessionID string, input, output, cached int, cost float64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.Exec(`
		UPDATE sessions SET
			total_input_tokens  = total_input_tokens  + ?,
			total_output_tokens = total_output_tokens + ?,
			total_cached_tokens = total_cached_tokens + ?,
			total_cost_usd      = total_cost_usd      + ?,
			updated_at          = ?
		WHERE id = ?`,
		input, output, cached, cost, now, sessionID,
	)
	return err
}

// SetResumeID persists a provider-side session ID for an agent within
// a session. Used for cross-restart resume: when ag3nts restarts with
// --resume, it can pass the stored provider ID to the agent subprocess.
func (d *DB) SetResumeID(sessionID, agentName, providerID string) error {
	// Read current resume_ids JSON.
	var resumeJSON string
	err := d.db.QueryRow(`SELECT resume_ids FROM sessions WHERE id = ?`, sessionID).Scan(&resumeJSON)
	if err != nil {
		return fmt.Errorf("read resume_ids: %w", err)
	}
	ids := make(map[string]string)
	_ = json.Unmarshal([]byte(resumeJSON), &ids)
	ids[agentName] = providerID
	data, _ := json.Marshal(ids)
	_, err = d.db.Exec(`UPDATE sessions SET resume_ids = ?, updated_at = ? WHERE id = ?`,
		string(data), time.Now().UTC().Format(time.RFC3339), sessionID)
	return err
}

// GetResumeIDs returns the stored provider-side session IDs for a session.
func (d *DB) GetResumeIDs(sessionID string) (map[string]string, error) {
	var resumeJSON string
	err := d.db.QueryRow(`SELECT resume_ids FROM sessions WHERE id = ?`, sessionID).Scan(&resumeJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make(map[string]string)
	_ = json.Unmarshal([]byte(resumeJSON), &ids)
	return ids, nil
}

// ListSessions returns sessions ordered by most recent first.
func (d *DB) ListSessions(limit int) ([]*SessionRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.db.Query(`
		SELECT id, name, working_dir, primary_agent, status,
		       created_at, updated_at,
		       total_input_tokens, total_output_tokens, total_cached_tokens, total_cost_usd,
		       resume_ids
		FROM sessions ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*SessionRecord
	for rows.Next() {
		var rec SessionRecord
		var createdStr, updatedStr, resumeJSON string
		if err := rows.Scan(
			&rec.ID, &rec.Name, &rec.WorkingDir, &rec.PrimaryAgent, &rec.Status,
			&createdStr, &updatedStr,
			&rec.TotalInputTokens, &rec.TotalOutputTokens, &rec.TotalCachedTokens, &rec.TotalCostUSD,
			&resumeJSON,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		rec.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		rec.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
		rec.ResumeIDs = make(map[string]string)
		_ = json.Unmarshal([]byte(resumeJSON), &rec.ResumeIDs)
		sessions = append(sessions, &rec)
	}
	return sessions, rows.Err()
}

// AgentTokenSummary holds per-agent token usage for a session.
type AgentTokenSummary struct {
	Agent        string
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	CostUSD      float64
	TaskCount    int
}

// TokensByAgent returns per-agent token summaries for a session.
func (d *DB) TokensByAgent(sessionID string) ([]*AgentTokenSummary, error) {
	rows, err := d.db.Query(`
		SELECT agent,
		       SUM(input_tokens) AS input_tokens,
		       SUM(output_tokens) AS output_tokens,
		       SUM(cached_tokens) AS cached_tokens,
		       SUM(cost_usd) AS cost_usd,
		       COUNT(*) AS task_count
		FROM tasks
		WHERE session_id = ? AND agent != ''
		GROUP BY agent
		ORDER BY cost_usd DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("tokens by agent: %w", err)
	}
	defer rows.Close()

	var summaries []*AgentTokenSummary
	for rows.Next() {
		var s AgentTokenSummary
		if err := rows.Scan(&s.Agent, &s.InputTokens, &s.OutputTokens, &s.CachedTokens, &s.CostUSD, &s.TaskCount); err != nil {
			return nil, fmt.Errorf("scan agent summary: %w", err)
		}
		summaries = append(summaries, &s)
	}
	return summaries, rows.Err()
}
