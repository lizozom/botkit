package store

import (
	"path/filepath"
	"testing"
)

func TestOpenSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE t (k TEXT PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES ('a','1')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var v string
	if err := db.QueryRow(`SELECT v FROM t WHERE k='a'`).Scan(&v); err != nil {
		t.Fatalf("select: %v", err)
	}
	if v != "1" {
		t.Errorf("v = %q, want 1", v)
	}

	// WAL mode should be active from the DSN pragma.
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("pragma journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

func TestOpenSQLiteBadPath(t *testing.T) {
	if _, err := OpenSQLite("/nonexistent-dir-xyz/session.db"); err == nil {
		t.Error("expected error opening in a nonexistent directory")
	}
}
