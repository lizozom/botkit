package schedule

import (
	"testing"
	"time"
)

func TestEvery(t *testing.T) {
	s := Every(5 * time.Minute)
	if got := s.UntilNext(time.Now()); got != 5*time.Minute {
		t.Errorf("Every UntilNext = %v, want 5m", got)
	}
	if s.IsDaily() {
		t.Error("Every is not daily")
	}
}

func TestEveryJitteredWithinBounds(t *testing.T) {
	s := EveryJittered(5*time.Minute, 2*time.Minute)
	min, max := s.intervalBounds()
	for i := 0; i < 200; i++ {
		d := s.UntilNext(time.Now())
		if d < min || d > max {
			t.Fatalf("jittered delay %v out of [%v,%v]", d, min, max)
		}
	}
}

func TestDailyAtToday(t *testing.T) {
	s := DailyAt(9, "Asia/Jerusalem")
	if !s.IsDaily() {
		t.Fatal("DailyAt should be daily")
	}
	// 07:00 local → next fire is 09:00 today (2h away).
	now := time.Date(2026, 7, 13, 7, 0, 0, 0, s.loc)
	if got := s.UntilNext(now); got != 2*time.Hour {
		t.Errorf("UntilNext at 07:00 = %v, want 2h", got)
	}
}

func TestDailyAtRollsToTomorrow(t *testing.T) {
	s := DailyAt(9, "Asia/Jerusalem")
	// 10:00 local, past 09:00 → next is 09:00 tomorrow (23h away).
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, s.loc)
	if got := s.UntilNext(now); got != 23*time.Hour {
		t.Errorf("UntilNext at 10:00 = %v, want 23h", got)
	}
}

func TestDailyAtExactHourRolls(t *testing.T) {
	s := DailyAt(9, "UTC")
	now := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)
	// exactly 09:00 → not After → rolls to tomorrow (24h), never fires twice.
	if got := s.UntilNext(now); got != 24*time.Hour {
		t.Errorf("UntilNext at exactly 09:00 = %v, want 24h", got)
	}
}

func TestDateKey(t *testing.T) {
	s := DailyAt(9, "UTC")
	got := s.DateKey(time.Date(2026, 7, 13, 23, 30, 0, 0, time.UTC))
	if got != "2026-07-13" {
		t.Errorf("DateKey = %q, want 2026-07-13", got)
	}
}

func TestDailyAtBadTZFallsBackUTC(t *testing.T) {
	s := DailyAt(9, "Not/AZone")
	if s.loc != time.UTC {
		t.Errorf("bad tz should fall back to UTC, got %v", s.loc)
	}
}
