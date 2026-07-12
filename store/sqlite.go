// Package store owns only cross-cutting persistence for botkit: the WhatsApp
// session-DB open (below), and — in later phases — a generic metadata KV
// (scheduler idempotency, webauth nonces, last-fired dates) and scheduler-run
// bookkeeping. Apps own all domain tables; SQLite stays their source of truth.
// See ../SPEC.md section 11.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (registers "sqlite")
)

// OpenSQLite opens a SQLite database with botkit's hardened pragmas: WAL
// journaling, a 5s busy timeout (brief lock contention retries instead of
// failing outright), and foreign keys on. These match what whatsmeow's
// sqlstore expects, so the same handle backs the WhatsApp session store.
//
// It pings once so a bad path/permission fails here rather than on first use.
func OpenSQLite(dbPath string) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		dbPath,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dbPath, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", dbPath, err)
	}
	return db, nil
}
