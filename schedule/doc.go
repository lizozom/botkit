// Package schedule is the scheduled-job kernel behind OnSchedule: tick, jitter,
// per-job overlap guard, daily-once idempotency across restarts, connected-
// gating, and staggered starts. Schedule kinds: Every, EveryJittered, DailyAt.
//
// Not yet implemented — see ../SPEC.md section 6.2. Lands in Phase 3.
package schedule
