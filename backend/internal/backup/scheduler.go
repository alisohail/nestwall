package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/platform/clock"
)

const (
	// DefaultPollInterval is how often the scheduler checks whether a backup is due.
	DefaultPollInterval = time.Minute

	// staleAfter is how long the last good backup may be before parents are told.
	staleAfter = 3 * 24 * time.Hour

	kindScheduled     = "scheduled"
	kindBackupFailed  = "backup_failed"
	kindBackupStale   = "backup_stale"
	failedBodyMessage = "The scheduled backup failed, so no new archive was kept. Open Settings > Data and backups to see why."
	staleBodyMessage  = "There has been no good backup for 3 days. Open Settings > Data and backups to make one."
	staleNoticeWindow = 24 * time.Hour
)

// Scheduler takes one backup a day at the hour set in Settings > Data and backups, applies retention
// after it, and tells the parents when a backup fails or when the last good one is too old.
type Scheduler struct {
	db         *sql.DB
	clock      clock.Clock
	log        *slog.Logger
	dir        string
	dbKey      string
	avatarsDir string
}

// NewScheduler returns a scheduler that writes archives to dir.
func NewScheduler(db *sql.DB, clk clock.Clock, log *slog.Logger, dir, dbKey, avatarsDir string) *Scheduler {
	return &Scheduler{db: db, clock: clk, log: log, dir: dir, dbKey: dbKey, avatarsDir: avatarsDir}
}

// Run checks for a due backup right away and then on every interval. It returns when ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick runs the daily backup if the latest slot has passed without one, then checks whether the last
// good backup is too old. The hour and retention come from the settings on every tick, so a change
// applies without a restart. A failed backup is not retried until the next slot.
func (s *Scheduler) Tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	now := s.clock.Now().In(time.Local)

	settings, err := ReadSettings(ctx, s.db)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("backup settings check failed", slog.String("error", err.Error()))
		}
		return
	}
	ran, err := s.ranSince(ctx, scheduledSlot(now, settings.ScheduleHour))
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("backup schedule check failed", slog.String("error", err.Error()))
		}
		return
	}
	if !ran {
		s.runScheduled(ctx, now, settings.KeepCount)
	}
	s.checkStale(ctx, now)
}

// scheduledSlot returns the most recent hour:00 at or before now, in now's location.
func scheduledSlot(now time.Time, hour int) time.Time {
	slot := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if now.Before(slot) {
		slot = slot.AddDate(0, 0, -1)
	}
	return slot
}

// NextRun returns the next hour:00 after now, in local time. The status page shows it.
func NextRun(now time.Time, hour int) time.Time {
	return scheduledSlot(now.In(time.Local), hour).AddDate(0, 0, 1)
}

// ranSince reports whether a scheduled run was recorded at or after slot, whatever its outcome.
// Times are compared with julianday, because SQLite compares the stored text and the stored offsets
// can differ.
func (s *Scheduler) ranSince(ctx context.Context, slot time.Time) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM backup_runs WHERE kind = ? AND julianday(started_at) >= julianday(?)`, kindScheduled, slot.UTC(),
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("count scheduled runs: %w", err)
	}
	return n > 0, nil
}

// runScheduled takes the backup, encrypted when the parents have turned encryption on. On failure it
// tells the parents; a failure to read the passphrase is recorded as a failed run here, because Run
// never started.
func (s *Scheduler) runScheduled(ctx context.Context, now time.Time, keepCount int) {
	passphrase, err := activePassphrase(ctx, s.db, s.dbKey)
	if err != nil {
		recordFailedRun(ctx, s.db, kindScheduled, now, err)
		s.failed(ctx, now, err)
		return
	}

	res, err := Run(ctx, s.db, Options{
		Dir:        s.dir,
		DBKey:      s.dbKey,
		AvatarsDir: s.avatarsDir,
		Passphrase: passphrase,
		Kind:       kindScheduled,
		Now:        now,
	})
	if err != nil {
		s.failed(ctx, now, err)
		return
	}
	s.log.Info("scheduled backup written", slog.String("file", res.FileName), slog.Int64("size_bytes", res.SizeBytes))

	if _, err := ApplyRetention(s.dir, keepCount); err != nil && ctx.Err() == nil {
		s.log.Error("backup retention failed", slog.String("error", err.Error()))
	}
	if _, err := ApplyResetRetention(s.dir, now); err != nil && ctx.Err() == nil {
		s.log.Error("reset backup retention failed", slog.String("error", err.Error()))
	}
	if err := PruneRuns(ctx, s.db, keepCount, now); err != nil && ctx.Err() == nil {
		s.log.Error("backup history prune failed", slog.String("error", err.Error()))
	}
}

func (s *Scheduler) failed(ctx context.Context, now time.Time, err error) {
	if ctx.Err() != nil {
		return
	}
	s.log.Error("scheduled backup failed", slog.String("error", err.Error()))
	s.notifyParents(ctx, now, kindBackupFailed, failedBodyMessage)
}

// checkStale tells the parents once a day while the last good backup is older than staleAfter.
func (s *Scheduler) checkStale(ctx context.Context, now time.Time) {
	since, ok, err := s.referenceTime(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("backup age check failed", slog.String("error", err.Error()))
		}
		return
	}
	if !ok || now.Sub(since) < staleAfter {
		return
	}

	var recent int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE kind = ? AND julianday(created_at) >= julianday(?)`,
		kindBackupStale, now.Add(-staleNoticeWindow).UTC(),
	).Scan(&recent); err != nil {
		if ctx.Err() == nil {
			s.log.Error("backup notice check failed", slog.String("error", err.Error()))
		}
		return
	}
	if recent > 0 {
		return
	}
	s.notifyParents(ctx, now, kindBackupStale, staleBodyMessage)
}

// referenceTime is when the last good backup finished. With none, it is when the first attempt
// started, so the warning is not raised before the backups have had a chance to run.
func (s *Scheduler) referenceTime(ctx context.Context) (time.Time, bool, error) {
	var at time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT finished_at FROM backup_runs WHERE status = 'ok' ORDER BY julianday(finished_at) DESC LIMIT 1`,
	).Scan(&at)
	if err == nil {
		return at, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, fmt.Errorf("load last good backup: %w", err)
	}

	err = s.db.QueryRowContext(ctx,
		`SELECT started_at FROM backup_runs ORDER BY julianday(started_at) ASC LIMIT 1`,
	).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("load first backup attempt: %w", err)
	}
	return at, true, nil
}

// notifyParents writes an in-app notice to every active parent in every family. A backup covers the
// whole instance, so each parent hears about it. The body is English, as the other notices are.
func (s *Scheduler) notifyParents(ctx context.Context, now time.Time, kind, body string) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.family_id, m.id FROM family_members m
		 JOIN roles r ON r.id = m.role_id
		 JOIN users u ON u.id = m.user_id
		 WHERE r.name = 'Parent' AND m.status = 'active' AND u.is_child = 0`)
	if err != nil {
		s.log.Error("load parents for backup notice", slog.String("error", err.Error()))
		return
	}
	type recipient struct{ familyID, memberID string }
	var recipients []recipient
	for rows.Next() {
		var r recipient
		if err := rows.Scan(&r.familyID, &r.memberID); err != nil {
			rows.Close()
			s.log.Error("scan parent for backup notice", slog.String("error", err.Error()))
			return
		}
		recipients = append(recipients, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.log.Error("load parents for backup notice", slog.String("error", err.Error()))
		return
	}

	for _, r := range recipients {
		if _, err := notifypolicy.Notify(ctx, s.db, notifypolicy.Notice{
			FamilyID: r.familyID, RecipientID: r.memberID, Kind: kind, Body: body,
		}, now.UTC()); err != nil && ctx.Err() == nil {
			s.log.Error("insert backup notice", slog.String("error", err.Error()))
		}
	}
}
