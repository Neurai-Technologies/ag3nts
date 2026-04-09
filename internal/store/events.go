package store

import (
	"fmt"
	"time"
)

// EventRecord maps to the agent_events table.
type EventRecord struct {
	ID        int64
	SessionID string
	TaskID    string
	Agent     string
	Kind      string
	Content   string
	Metadata  string // JSON
	Seq       int64
	CreatedAt time.Time
}

// InsertEvent stores an agent event. Seq should be a monotonically
// increasing number per session (use the bus sequence number).
func (d *DB) InsertEvent(rec *EventRecord) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.Exec(`
		INSERT INTO agent_events (session_id, task_id, agent, kind, content, metadata, seq, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.SessionID, rec.TaskID, rec.Agent, rec.Kind, rec.Content, rec.Metadata, rec.Seq, now,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

// EventsSince returns events for a session with seq > afterSeq, ordered by seq.
func (d *DB) EventsSince(sessionID string, afterSeq int64, limit int) ([]*EventRecord, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := d.db.Query(`
		SELECT id, session_id, task_id, agent, kind, content, metadata, seq, created_at
		FROM agent_events
		WHERE session_id = ? AND seq > ?
		ORDER BY seq ASC
		LIMIT ?`, sessionID, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("events since: %w", err)
	}
	defer rows.Close()

	var events []*EventRecord
	for rows.Next() {
		var rec EventRecord
		var createdStr string
		if err := rows.Scan(
			&rec.ID, &rec.SessionID, &rec.TaskID, &rec.Agent, &rec.Kind,
			&rec.Content, &rec.Metadata, &rec.Seq, &createdStr,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		rec.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		events = append(events, &rec)
	}
	return events, rows.Err()
}

// EventCount returns the total number of events for a session.
func (d *DB) EventCount(sessionID string) (int, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM agent_events WHERE session_id = ?`, sessionID).Scan(&count)
	return count, err
}
