package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
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

// Consume atomically reads and removes a key, reporting whether it was there.
// Exactly one concurrent caller can observe ok=true for a given key — that
// guarantee is what makes a webauth magic link single-use, so this must never
// be relaxed into a Get followed by a Delete.
func (k *KV) Consume(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := k.db.QueryRowContext(ctx,
		`DELETE FROM botkit_kv WHERE key = ? RETURNING value`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("kv consume %q: %w", key, err)
	}
	return v, true, nil
}

// Delete removes a key (no error if absent).
func (k *KV) Delete(ctx context.Context, key string) error {
	if _, err := k.db.ExecContext(ctx, `DELETE FROM botkit_kv WHERE key = ?`, key); err != nil {
		return fmt.Errorf("kv delete %q: %w", key, err)
	}
	return nil
}

// DeleteExpired removes every key under prefix whose last write is older than
// age, returning how many went. Without it, keys with a natural lifetime —
// webauth nonces that are minted far more often than they are redeemed — would
// accumulate in the KV forever.
//
// Resolution is one second: updated_at is a SQLite datetime, so a sub-second
// age cannot be expressed and rounds to zero. This is a garbage collector for
// values whose real lifetimes are minutes; anything needing finer expiry must
// carry its own timestamp and check it on read (webauth does both).
func (k *KV) DeleteExpired(ctx context.Context, prefix string, age time.Duration) (int64, error) {
	cutoff := fmt.Sprintf("-%d seconds", int64(age.Seconds()))
	res, err := k.db.ExecContext(ctx,
		`DELETE FROM botkit_kv WHERE key LIKE ? || '%' AND updated_at < datetime('now', ?)`,
		prefix, cutoff)
	if err != nil {
		return 0, fmt.Errorf("kv delete expired %q: %w", prefix, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil // the delete succeeded; the count is a nicety
	}
	return n, nil
}

// Close closes the underlying DB.
func (k *KV) Close() error { return k.db.Close() }
