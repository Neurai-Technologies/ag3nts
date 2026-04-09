package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// TaskRecord maps to the tasks table.
type TaskRecord struct {
	ID           string
	SessionID    string
	Agent        string
	Type         string
	Description  string
	Status       string // pending, queued, running, completed, failed
	DependsOn    []string
	ContextFrom  []string
	ResultOutput string
	ResultError  string
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	CostUSD      float64
	DurationMs   int64
	RoutingRule  string
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
}

// CreateTask inserts a new task record.
func (d *DB) CreateTask(rec *TaskRecord) error {
	depsJSON, _ := json.Marshal(rec.DependsOn)
	ctxJSON, _ := json.Marshal(rec.ContextFrom)
	now := time.Now().UTC().Format(time.RFC3339)
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	_, err := d.db.Exec(`
		INSERT INTO tasks (id, session_id, agent, type, description, status, depends_on, context_from, routing_rule, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.SessionID, rec.Agent, rec.Type, rec.Description,
		rec.Status, string(depsJSON), string(ctxJSON), rec.RoutingRule, now,
	)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	return nil
}

// UpdateTaskStatus sets the task status and relevant timestamp.
func (d *DB) UpdateTaskStatus(id, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	switch status {
	case "running":
		_, err := d.db.Exec(`UPDATE tasks SET status = ?, started_at = ? WHERE id = ?`, status, now, id)
		return err
	case "completed", "failed":
		_, err := d.db.Exec(`UPDATE tasks SET status = ?, completed_at = ? WHERE id = ?`, status, now, id)
		return err
	default:
		_, err := d.db.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, status, id)
		return err
	}
}

// TokenRecord holds token usage for a completed task.
type TokenRecord struct {
	InputTokens  int
	OutputTokens int
	CachedTokens int
	CostUSD      float64
}

// UpdateTaskResult stores the result of a completed task.
func (d *DB) UpdateTaskResult(id string, agent, output, errStr string, tokens TokenRecord, durationMs int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	status := "completed"
	if errStr != "" {
		status = "failed"
	}
	_, err := d.db.Exec(`
		UPDATE tasks SET
			agent = ?, status = ?, result_output = ?, result_error = ?,
			input_tokens = ?, output_tokens = ?, cached_tokens = ?, cost_usd = ?,
			duration_ms = ?, completed_at = ?
		WHERE id = ?`,
		agent, status, output, errStr,
		tokens.InputTokens, tokens.OutputTokens, tokens.CachedTokens, tokens.CostUSD,
		durationMs, now, id,
	)
	return err
}

// GetTask retrieves a task by ID.
func (d *DB) GetTask(id string) (*TaskRecord, error) {
	row := d.db.QueryRow(`
		SELECT id, session_id, agent, type, description, status,
		       depends_on, context_from, result_output, result_error,
		       input_tokens, output_tokens, cached_tokens, cost_usd,
		       duration_ms, routing_rule, created_at, started_at, completed_at
		FROM tasks WHERE id = ?`, id)
	return scanTask(row)
}

// ListTasks returns all tasks for a session in creation order.
func (d *DB) ListTasks(sessionID string) ([]*TaskRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, session_id, agent, type, description, status,
		       depends_on, context_from, result_output, result_error,
		       input_tokens, output_tokens, cached_tokens, cost_usd,
		       duration_ms, routing_rule, created_at, started_at, completed_at
		FROM tasks WHERE session_id = ? ORDER BY created_at`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*TaskRecord
	for rows.Next() {
		rec, err := scanTaskRows(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, rec)
	}
	return tasks, rows.Err()
}

// scanTask scans a single *sql.Row into a TaskRecord.
func scanTask(row *sql.Row) (*TaskRecord, error) {
	var rec TaskRecord
	var depsJSON, ctxJSON string
	var createdStr string
	var startedStr, completedStr sql.NullString

	err := row.Scan(
		&rec.ID, &rec.SessionID, &rec.Agent, &rec.Type, &rec.Description, &rec.Status,
		&depsJSON, &ctxJSON, &rec.ResultOutput, &rec.ResultError,
		&rec.InputTokens, &rec.OutputTokens, &rec.CachedTokens, &rec.CostUSD,
		&rec.DurationMs, &rec.RoutingRule, &createdStr, &startedStr, &completedStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan task: %w", err)
	}

	_ = json.Unmarshal([]byte(depsJSON), &rec.DependsOn)
	_ = json.Unmarshal([]byte(ctxJSON), &rec.ContextFrom)
	rec.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	if startedStr.Valid {
		t, _ := time.Parse(time.RFC3339, startedStr.String)
		rec.StartedAt = &t
	}
	if completedStr.Valid {
		t, _ := time.Parse(time.RFC3339, completedStr.String)
		rec.CompletedAt = &t
	}
	return &rec, nil
}

// scanTaskRows scans a single row from *sql.Rows into a TaskRecord.
func scanTaskRows(rows *sql.Rows) (*TaskRecord, error) {
	var rec TaskRecord
	var depsJSON, ctxJSON string
	var createdStr string
	var startedStr, completedStr sql.NullString

	err := rows.Scan(
		&rec.ID, &rec.SessionID, &rec.Agent, &rec.Type, &rec.Description, &rec.Status,
		&depsJSON, &ctxJSON, &rec.ResultOutput, &rec.ResultError,
		&rec.InputTokens, &rec.OutputTokens, &rec.CachedTokens, &rec.CostUSD,
		&rec.DurationMs, &rec.RoutingRule, &createdStr, &startedStr, &completedStr,
	)
	if err != nil {
		return nil, fmt.Errorf("scan task row: %w", err)
	}

	_ = json.Unmarshal([]byte(depsJSON), &rec.DependsOn)
	_ = json.Unmarshal([]byte(ctxJSON), &rec.ContextFrom)
	rec.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	if startedStr.Valid {
		t, _ := time.Parse(time.RFC3339, startedStr.String)
		rec.StartedAt = &t
	}
	if completedStr.Valid {
		t, _ := time.Parse(time.RFC3339, completedStr.String)
		rec.CompletedAt = &t
	}
	return &rec, nil
}
