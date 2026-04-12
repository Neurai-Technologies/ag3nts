package store

import "fmt"

const currentSchemaVersion = 4

// schema defines the DDL for all tables at schema version 1.
const schema = `
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL DEFAULT '',
    working_dir         TEXT NOT NULL DEFAULT '',
    primary_agent       TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'active',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    total_input_tokens  INTEGER NOT NULL DEFAULT 0,
    total_output_tokens INTEGER NOT NULL DEFAULT 0,
    total_cached_tokens INTEGER NOT NULL DEFAULT 0,
    total_cost_usd      REAL NOT NULL DEFAULT 0.0
);

CREATE TABLE IF NOT EXISTS tasks (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL REFERENCES sessions(id),
    agent           TEXT NOT NULL DEFAULT '',
    type            TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending',
    depends_on      TEXT NOT NULL DEFAULT '[]',
    context_from    TEXT NOT NULL DEFAULT '[]',
    result_output   TEXT NOT NULL DEFAULT '',
    result_error    TEXT NOT NULL DEFAULT '',
    input_tokens    INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    cached_tokens   INTEGER NOT NULL DEFAULT 0,
    cost_usd        REAL NOT NULL DEFAULT 0.0,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    routing_rule    TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    started_at      TEXT,
    completed_at    TEXT
);

CREATE INDEX IF NOT EXISTS idx_tasks_session ON tasks(session_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);

CREATE TABLE IF NOT EXISTS agent_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT NOT NULL REFERENCES sessions(id),
    task_id     TEXT NOT NULL DEFAULT '',
    agent       TEXT NOT NULL,
    kind        TEXT NOT NULL,
    content     TEXT NOT NULL DEFAULT '',
    metadata    TEXT NOT NULL DEFAULT '{}',
    seq         INTEGER NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_session_seq ON agent_events(session_id, seq);

CREATE TABLE IF NOT EXISTS memories (
    id          TEXT PRIMARY KEY,
    category    TEXT NOT NULL,
    scope       TEXT NOT NULL DEFAULT '',
    key         TEXT NOT NULL,
    content     TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_memories_category_scope ON memories(category, scope);

CREATE TABLE IF NOT EXISTS schedules (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    cron        TEXT NOT NULL,
    recipe      TEXT NOT NULL,
    params      TEXT NOT NULL DEFAULT '{}',
    enabled     INTEGER NOT NULL DEFAULT 1,
    last_run    TEXT,
    next_run    TEXT,
    created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS context_chunks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT NOT NULL,
    task_id     TEXT NOT NULL DEFAULT '',
    agent       TEXT NOT NULL DEFAULT '',
    kind        TEXT NOT NULL DEFAULT '',
    content     TEXT NOT NULL,
    token_count INTEGER NOT NULL DEFAULT 0,
    keywords    TEXT NOT NULL DEFAULT '',
    seq         INTEGER NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_context_session_seq ON context_chunks(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_context_created ON context_chunks(created_at);
`

// migrate runs idempotent schema migrations.
func (d *DB) migrate() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Check if schema_version table exists.
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check schema_version: %w", err)
	}

	if count == 0 {
		// Fresh database — apply full schema.
		if _, err := d.db.Exec(schema); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		if _, err := d.db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, currentSchemaVersion); err != nil {
			return fmt.Errorf("set schema version: %w", err)
		}
		return nil
	}

	// Existing database — check version and apply incremental migrations.
	var version int
	if err := d.db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if version >= currentSchemaVersion {
		return nil // already up to date
	}

	if version < 2 {
		_, err = d.db.Exec(`
			CREATE TABLE IF NOT EXISTS memories (
				id          TEXT PRIMARY KEY,
				category    TEXT NOT NULL,
				scope       TEXT NOT NULL DEFAULT '',
				key         TEXT NOT NULL,
				content     TEXT NOT NULL,
				source      TEXT NOT NULL DEFAULT '',
				created_at  TEXT NOT NULL,
				updated_at  TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_memories_category_scope ON memories(category, scope);
		`)
		if err != nil {
			return fmt.Errorf("migrate v2 (memories table): %w", err)
		}
	}

	if version < 3 {
		_, err = d.db.Exec(`
			CREATE TABLE IF NOT EXISTS schedules (
				id          TEXT PRIMARY KEY,
				name        TEXT NOT NULL DEFAULT '',
				cron        TEXT NOT NULL,
				recipe      TEXT NOT NULL,
				params      TEXT NOT NULL DEFAULT '{}',
				enabled     INTEGER NOT NULL DEFAULT 1,
				last_run    TEXT,
				next_run    TEXT,
				created_at  TEXT NOT NULL
			);
		`)
		if err != nil {
			return fmt.Errorf("migrate v3 (schedules table): %w", err)
		}
	}

	if version < 4 {
		_, err = d.db.Exec(`
			CREATE TABLE IF NOT EXISTS context_chunks (
				id          INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id  TEXT NOT NULL,
				task_id     TEXT NOT NULL DEFAULT '',
				agent       TEXT NOT NULL DEFAULT '',
				kind        TEXT NOT NULL DEFAULT '',
				content     TEXT NOT NULL,
				token_count INTEGER NOT NULL DEFAULT 0,
				keywords    TEXT NOT NULL DEFAULT '',
				seq         INTEGER NOT NULL,
				created_at  TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_context_session_seq ON context_chunks(session_id, seq);
			CREATE INDEX IF NOT EXISTS idx_context_created ON context_chunks(created_at);
		`)
		if err != nil {
			return fmt.Errorf("migrate v4 (context_chunks table): %w", err)
		}
	}

	_, err = d.db.Exec(`UPDATE schema_version SET version = ?`, currentSchemaVersion)
	return err
}
