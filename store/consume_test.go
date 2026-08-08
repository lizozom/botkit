package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestConsume(t *testing.T) {
	ctx := context.Background()
	kv, err := NewKV(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("NewKV: %v", err)
	}
	defer kv.Close()

	if _, ok, err := kv.Consume(ctx, "missing"); ok || err != nil {
		t.Errorf("Consume(missing) = _,%v,%v; want false,nil", ok, err)
	}

	if err := kv.Set(ctx, "nonce", "payload"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := kv.Consume(ctx, "nonce")
	if err != nil || !ok || v != "payload" {
		t.Fatalf("Consume = %q,%v,%v; want payload,true,nil", v, ok, err)
	}
	if _, ok, _ := kv.Get(ctx, "nonce"); ok {
		t.Error("key survived Consume")
	}
	if _, ok, _ := kv.Consume(ctx, "nonce"); ok {
		t.Error("second Consume succeeded — single-use is broken")
	}
}

// The single-use property of a magic link rests entirely on this: many racing
// redemptions of one nonce, exactly one winner.
func TestConsumeIsSingleWinnerUnderRace(t *testing.T) {
	ctx := context.Background()
	kv, err := NewKV(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("NewKV: %v", err)
	}
	defer kv.Close()

	if err := kv.Set(ctx, "nonce", "payload"); err != nil {
		t.Fatal(err)
	}

	const racers = 16
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		wins  int
		fails []error
	)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			v, ok, err := kv.Consume(ctx, "nonce")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				fails = append(fails, err)
			case ok && v == "payload":
				wins++
			case ok:
				fails = append(fails, nil) // won but wrong payload
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(fails) != 0 {
		t.Errorf("%d racers errored, first: %v", len(fails), fails[0])
	}
	if wins != 1 {
		t.Errorf("winners = %d; want exactly 1", wins)
	}
}
