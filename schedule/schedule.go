// Package schedule describes when a bot's timed jobs (OnSchedule) fire. It is
// pure timing math — the run loop, overlap guard, connected-gating, and
// daily-idempotency live in package bot. See ../SPEC.md §6.2.
//
// Kinds:
//   - Every(d)              — fixed interval.
//   - EveryJittered(d, j)   — interval ± up-to-j random jitter (avoids
//     machine-regular timing; part of the anti-abuse posture).
//   - DailyAt(hour, tz)     — once per calendar day at hour:00 in tz. Combined
//     with bot's KV marker, fires once per day even across restarts.
package schedule

import (
	"math/rand/v2"
	"time"
)

type kind int

const (
	kindInterval kind = iota
	kindDaily
)

// Spec is an opaque schedule. Build one with Every / EveryJittered / DailyAt.
type Spec struct {
	kind   kind
	every  time.Duration
	jitter time.Duration
	hour   int
	loc    *time.Location
}

// Every fires on a fixed interval.
func Every(d time.Duration) Spec { return Spec{kind: kindInterval, every: d} }

// EveryJittered fires every d, ± a random amount up to jitter.
func EveryJittered(d, jitter time.Duration) Spec {
	return Spec{kind: kindInterval, every: d, jitter: jitter}
}

// DailyAt fires once per day at hour:00 in the named IANA timezone (e.g.
// "Asia/Jerusalem"). An unknown tz falls back to UTC.
func DailyAt(hour int, tz string) Spec {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	if hour < 0 {
		hour = 0
	}
	if hour > 23 {
		hour = 23
	}
	return Spec{kind: kindDaily, hour: hour, loc: loc}
}

// UntilNext returns the delay from now until the next fire.
func (s Spec) UntilNext(now time.Time) time.Duration {
	if s.kind == kindDaily {
		n := now.In(s.loc)
		next := time.Date(n.Year(), n.Month(), n.Day(), s.hour, 0, 0, 0, s.loc)
		if !next.After(n) {
			next = next.AddDate(0, 0, 1)
		}
		return next.Sub(n)
	}
	d := s.every
	if s.jitter > 0 {
		// uniform in [-jitter, +jitter]
		d += time.Duration(rand.Int64N(int64(2*s.jitter)+1)) - s.jitter
	}
	if d < 0 {
		d = 0
	}
	return d
}

// IsDaily reports whether this is a once-per-day schedule (needs idempotency).
func (s Spec) IsDaily() bool { return s.kind == kindDaily }

// DateKey is the per-day idempotency key ("YYYY-MM-DD" in the spec's tz).
func (s Spec) DateKey(t time.Time) string {
	loc := s.loc
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("2006-01-02")
}

// interval bounds, exported for tests.
func (s Spec) intervalBounds() (min, max time.Duration) {
	return s.every - s.jitter, s.every + s.jitter
}
