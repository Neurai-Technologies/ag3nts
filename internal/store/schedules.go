package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ScheduleRecord maps to the schedules table.
type ScheduleRecord struct {
	ID        string
	Name      string
	Cron      string
	Recipe    string
	Params    map[string]string
	Enabled   bool
	LastRun   time.Time
	NextRun   time.Time
	CreatedAt time.Time
}

// CreateSchedule inserts a new schedule record.
func (d *DB) CreateSchedule(rec *ScheduleRecord) error {
	paramsJSON, _ := json.Marshal(rec.Params)
	now := time.Now().UTC().Format(time.RFC3339)
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	enabled := 0
	if rec.Enabled {
		enabled = 1
	}
	_, err := d.db.Exec(`
		INSERT INTO schedules (id, name, cron, recipe, params, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.Name, rec.Cron, rec.Recipe, string(paramsJSON), enabled, now,
	)
	if err != nil {
		return fmt.Errorf("insert schedule: %w", err)
	}
	return nil
}

// GetSchedule retrieves a schedule by ID.
func (d *DB) GetSchedule(id string) (*ScheduleRecord, error) {
	row := d.db.QueryRow(`
		SELECT id, name, cron, recipe, params, enabled, last_run, next_run, created_at
		FROM schedules WHERE id = ?`, id)
	return scanSchedule(row)
}

// ListSchedules returns all schedules.
func (d *DB) ListSchedules() ([]*ScheduleRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, name, cron, recipe, params, enabled, last_run, next_run, created_at
		FROM schedules ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()

	var schedules []*ScheduleRecord
	for rows.Next() {
		rec, err := scanScheduleRows(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, rec)
	}
	return schedules, rows.Err()
}

// DeleteSchedule removes a schedule by ID.
func (d *DB) DeleteSchedule(id string) error {
	_, err := d.db.Exec(`DELETE FROM schedules WHERE id = ?`, id)
	return err
}

// UpdateScheduleLastRun updates the last_run timestamp.
func (d *DB) UpdateScheduleLastRun(id string, t time.Time) error {
	_, err := d.db.Exec(`UPDATE schedules SET last_run = ? WHERE id = ?`,
		t.Format(time.RFC3339), id)
	return err
}

// UpdateScheduleNextRun updates the next_run timestamp.
func (d *DB) UpdateScheduleNextRun(id string, t time.Time) error {
	_, err := d.db.Exec(`UPDATE schedules SET next_run = ? WHERE id = ?`,
		t.Format(time.RFC3339), id)
	return err
}

func scanSchedule(row *sql.Row) (*ScheduleRecord, error) {
	var rec ScheduleRecord
	var paramsJSON string
	var enabled int
	var createdStr string
	var lastRunStr, nextRunStr sql.NullString

	err := row.Scan(&rec.ID, &rec.Name, &rec.Cron, &rec.Recipe, &paramsJSON,
		&enabled, &lastRunStr, &nextRunStr, &createdStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan schedule: %w", err)
	}

	rec.Enabled = enabled != 0
	_ = json.Unmarshal([]byte(paramsJSON), &rec.Params)
	rec.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	if lastRunStr.Valid {
		rec.LastRun, _ = time.Parse(time.RFC3339, lastRunStr.String)
	}
	if nextRunStr.Valid {
		rec.NextRun, _ = time.Parse(time.RFC3339, nextRunStr.String)
	}
	return &rec, nil
}

func scanScheduleRows(rows *sql.Rows) (*ScheduleRecord, error) {
	var rec ScheduleRecord
	var paramsJSON string
	var enabled int
	var createdStr string
	var lastRunStr, nextRunStr sql.NullString

	err := rows.Scan(&rec.ID, &rec.Name, &rec.Cron, &rec.Recipe, &paramsJSON,
		&enabled, &lastRunStr, &nextRunStr, &createdStr)
	if err != nil {
		return nil, fmt.Errorf("scan schedule row: %w", err)
	}

	rec.Enabled = enabled != 0
	_ = json.Unmarshal([]byte(paramsJSON), &rec.Params)
	rec.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	if lastRunStr.Valid {
		rec.LastRun, _ = time.Parse(time.RFC3339, lastRunStr.String)
	}
	if nextRunStr.Valid {
		rec.NextRun, _ = time.Parse(time.RFC3339, nextRunStr.String)
	}
	return &rec, nil
}
