// Package scheduler runs the background worker that keeps occurrences, their past due and missed
// status, and expiry penalty intents up to date.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"nestwall/backend/internal/badges"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/rewards"
	"nestwall/backend/internal/tasks"
)

// DefaultInterval is how often the worker runs when the server is up.
const DefaultInterval = time.Minute

// Worker runs tasks.RunSchedule on a fixed interval, and syncs linked calendars when it has a LinkSyncer.
type Worker struct {
	db       *sql.DB
	clock    clock.Clock
	log      *slog.Logger
	interval time.Duration
	links    LinkSyncer
}

// LinkSyncer refreshes the linked calendars that are due. The calendar package implements it; the
// scheduler does not import that package.
type LinkSyncer interface {
	SyncDueLinks(ctx context.Context) (int, error)
}

// New returns a worker. A non-positive interval uses DefaultInterval.
func New(db *sql.DB, clk clock.Clock, log *slog.Logger, interval time.Duration) *Worker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Worker{db: db, clock: clk, log: log, interval: interval}
}

// WithLinkSync makes each tick also refresh the linked calendars that are due.
func (w *Worker) WithLinkSync(s LinkSyncer) *Worker {
	w.links = s
	return w
}

// RunOnce brings the schedule up to the clock's current time, then posts the penalty for each
// new penalty intent. Settlement is idempotent, so a failed run is retried on the next tick.
func (w *Worker) RunOnce(ctx context.Context) (tasks.ScheduleResult, error) {
	now := w.clock.Now()
	result, err := tasks.RunSchedule(ctx, w.db, now)
	if err != nil {
		return result, err
	}
	if _, err := rewards.SettlePenalties(ctx, w.db, now); err != nil {
		return result, fmt.Errorf("settle penalties: %w", err)
	}
	// Badges that approvals did not award (shares, redemptions, a day that closed). Once per child.
	if _, err := badges.EvaluateAll(ctx, w.db, now); err != nil {
		return result, fmt.Errorf("evaluate badges: %w", err)
	}
	return result, nil
}

// Run runs once immediately, which catches up on anything missed while the server was down, and
// then on every interval. It returns when ctx is cancelled. A failed run is logged and retried on
// the next tick.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		w.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// tick runs one pass. Linked calendars are synced after the schedule, so a slow fetch does not delay
// the schedule, and a failed schedule run does not skip them.
func (w *Worker) tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	w.runSchedule(ctx)
	w.syncLinks(ctx)
}

// runSchedule runs RunOnce and logs what changed.
func (w *Worker) runSchedule(ctx context.Context) {
	result, err := w.RunOnce(ctx)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Error("scheduler run failed", slog.String("error", err.Error()))
		}
		return
	}
	if result.Generated == 0 && result.PastDue == 0 && result.Missed == 0 && result.Penalties == 0 {
		return
	}
	w.log.Info("scheduler run",
		slog.Int("generated", result.Generated),
		slog.Int("past_due", result.PastDue),
		slog.Int("missed", result.Missed),
		slog.Int("penalties", result.Penalties),
	)
}

// syncLinks refreshes the due linked calendars, if any are wired in.
func (w *Worker) syncLinks(ctx context.Context) {
	if w.links == nil {
		return
	}
	n, err := w.links.SyncDueLinks(ctx)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Error("calendar link sync failed", slog.String("error", err.Error()))
		}
		return
	}
	if n > 0 {
		w.log.Info("calendar link sync", slog.Int("attempted", n))
	}
}
