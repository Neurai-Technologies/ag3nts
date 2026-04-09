// Package store provides SQLite-backed persistence for ag3nts sessions,
// tasks, agent events, and cross-agent memory. Uses modernc.org/sqlite
// (pure Go, no CGO) with WAL mode for concurrent reads.
package store

import (
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver
)

// DB is the central SQLite persistence layer for ag3nts.
type DB struct {
	db *sql.DB
	mu sync.Mutex // protects schema migrations only
}

// Config holds database connection parameters.
type Config struct {
	Path string // e.g. state/ag3nts.db
}

// Open creates or opens a SQLite database at the given path.
// Enables WAL mode for concurrent reads and sets a busy timeout.
func Open(cfg Config) (*DB, error) {
	dsn := cfg.Path + "?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", cfg.Path, err)
	}

	// SQLite supports only one writer — serialize all access through one
	// connection to avoid SQLITE_BUSY under concurrent goroutines.
	sqlDB.SetMaxOpenConns(1)

	// Verify connection works.
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	d := &DB{db: sqlDB}
	if err := d.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return d, nil
}

// Close shuts down the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// Tx runs fn inside a transaction. Commits on success, rolls back on error.
func (d *DB) Tx(fn func(tx *sql.Tx) error) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
