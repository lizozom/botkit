package store

import (
	"context"
	"database/sql"
	"fmt"
)

// KV is a tiny persistent string key-value store, botkit's metadata substrate:
// scheduler daily-idempotency markers, webauth nonces, last-fired dates. Apps
// keep their own domain tables elsewhere; this is framework bookkeeping only.
type KV struct {
	db *sql.DB
}

// NewKV opens (or creates) a KV-backed SQLite file and ensures its table.
func NewKV(dbPath string) (*KV, error) {
	db, err := OpenSQLite(dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS botkit_kv (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create botkit_kv: %w", err)
	}
	return &KV{db: db}, nil
}

// Get returns the value and whether the key exists.
func (k *KV) Get(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := k.db.QueryRowContext(ctx, `SELECT value FROM botkit_kv WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("kv get %q: %w", key, err)
	}
	return v, true, nil
}

// Set upserts a key.
func (k *KV) Set(ctx context.Context, key, value string) error {
	_, err := k.db.ExecContext(ctx, `INSERT INTO botkit_kv (key, value, updated_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value)
	if err != nil {
		return fmt.Errorf("kv set %q: %w", key, err)
	}
	return nil
}

// Delete removes a key (no error if absent).
func (k *KV) Delete(ctx context.Context, key string) error {
	if _, err := k.db.ExecContext(ctx, `DELETE FROM botkit_kv WHERE key = ?`, key); err != nil {
		return fmt.Errorf("kv delete %q: %w", key, err)
	}
	return nil
}

// Close closes the underlying DB.
func (k *KV) Close() error { return k.db.Close() }
