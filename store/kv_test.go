package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestKV(t *testing.T) {
	ctx := context.Background()
	kv, err := NewKV(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("NewKV: %v", err)
	}
	defer kv.Close()

	if _, ok, _ := kv.Get(ctx, "missing"); ok {
		t.Error("missing key should not exist")
	}
	if err := kv.Set(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := kv.Get(ctx, "k"); !ok || v != "v1" {
		t.Errorf("Get = %q,%v; want v1,true", v, ok)
	}
	// upsert
	if err := kv.Set(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := kv.Get(ctx, "k"); v != "v2" {
		t.Errorf("after upsert = %q, want v2", v)
	}
	// delete
	if err := kv.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := kv.Get(ctx, "k"); ok {
		t.Error("deleted key should not exist")
	}
}

func TestKVPersistsAcrossOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	kv1, err := NewKV(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = kv1.Set(ctx, "daily:audit", "2026-07-13")
	kv1.Close()

	kv2, err := NewKV(path) // reopen — simulate a restart
	if err != nil {
		t.Fatal(err)
	}
	defer kv2.Close()
	if v, ok, _ := kv2.Get(ctx, "daily:audit"); !ok || v != "2026-07-13" {
		t.Errorf("value did not persist across reopen: %q,%v", v, ok)
	}
}
