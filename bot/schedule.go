package bot

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/lizozom/botkit/schedule"
)

type scheduledJob struct {
	name string
	spec schedule.Spec
	fn   func(context.Context) error
}

// OnSchedule registers a named timed job. Register as many as needed; each runs
// on its own goroutine, started once the bot first connects. Guarantees:
// sequential per job (a run never overlaps its own next tick), jittered +
// staggered starts, and — for DailyAt — fires at most once per calendar day
// even across restarts (via the KV marker).
func (b *Bot) OnSchedule(name string, spec schedule.Spec, fn func(context.Context) error) {
	b.jobs = append(b.jobs, scheduledJob{name: name, spec: spec, fn: fn})
}

// startJobs launches every registered job. Called once, on first connect.
func (b *Bot) startJobs(ctx context.Context) {
	for i, j := range b.jobs {
		// Stagger starts so N jobs don't fire in one burst at boot.
		stagger := time.Duration(i)*5*time.Second + time.Duration(rand.Int64N(5000))*time.Millisecond
		go b.runJob(ctx, j, stagger)
	}
}

func (b *Bot) runJob(ctx context.Context, j scheduledJob, stagger time.Duration) {
	if !sleepCtx(ctx, stagger) {
		return
	}
	for {
		if !sleepCtx(ctx, j.spec.UntilNext(time.Now())) {
			return
		}
		b.fireJob(ctx, j)
	}
}

// fireJob runs a job once. Because runJob is a sequential loop (fire, then wait
// for the next tick), a slow run can never overlap the next one. DailyAt jobs
// are guarded by a per-day KV marker so a mid-day restart doesn't re-fire.
func (b *Bot) fireJob(ctx context.Context, j scheduledJob) {
	var dayKey string
	if j.spec.IsDaily() && b.kv != nil {
		dayKey = "sched:" + j.name + ":" + j.spec.DateKey(time.Now())
		if _, done, _ := b.kv.Get(ctx, dayKey); done {
			return // already fired today
		}
	}
	if err := j.fn(ctx); err != nil {
		slog.Error("botkit: scheduled job error",
			slog.String("job", j.name), slog.String("err", err.Error()))
		return // don't mark done on failure — let it retry next tick/day
	}
	if dayKey != "" {
		_ = b.kv.Set(ctx, dayKey, "1")
	}
}

// sleepCtx waits d, returning true if it elapsed and false if ctx was cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
