package backup

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestScheduler(conn *sql.DB, dir string, at time.Time) *Scheduler {
	return NewScheduler(conn, clock.FakeClock{Time: at}, quietLogger(), dir, testDBKey, filepath.Join(dir, "no-avatars"))
}

// sqlCount runs a COUNT query and returns the result.
func sqlCount(t *testing.T, conn *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// mustInsertRun records a backup attempt. A zero finishedAt leaves the run unfinished.
func mustInsertRun(t *testing.T, conn *sql.DB, status string, startedAt, finishedAt time.Time) {
	t.Helper()
	var finished any
	if !finishedAt.IsZero() {
		finished = finishedAt
	}
	if _, err := conn.Exec(
		`INSERT INTO backup_runs (id, kind, status, started_at, finished_at) VALUES (?, ?, ?, ?, ?)`,
		ids.New(), kindScheduled, status, startedAt, finished,
	); err != nil {
		t.Fatalf("insert backup run: %v", err)
	}
}

func TestScheduledSlot(t *testing.T) {
	day := func(h, m int) time.Time { return time.Date(2026, 10, 21, h, m, 0, 0, time.Local) }
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"before 2am uses yesterday's slot", day(1, 30), day(2, 0).AddDate(0, 0, -1)},
		{"exactly 2am is today's slot", day(2, 0), day(2, 0)},
		{"afternoon uses today's slot", day(15, 0), day(2, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := scheduledSlot(c.now, 2); !got.Equal(c.want) {
				t.Fatalf("slot = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSchedulerRunsOncePerDay(t *testing.T) {
	conn := openTestDB(t)
	dir := t.TempDir()
	day1 := time.Date(2026, 10, 21, 2, 30, 0, 0, time.Local)
	s := newTestScheduler(conn, dir, day1)

	s.Tick(context.Background())
	s.Tick(context.Background())
	s.clock = clock.FakeClock{Time: day1.Add(7 * time.Hour)}
	s.Tick(context.Background())
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_runs WHERE kind = 'scheduled' AND status = 'ok'`); n != 1 {
		t.Fatalf("runs on day 1 = %d, want 1", n)
	}

	s.clock = clock.FakeClock{Time: day1.AddDate(0, 0, 1).Add(-80 * time.Minute)}
	s.Tick(context.Background())
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_runs WHERE kind = 'scheduled'`); n != 1 {
		t.Fatalf("runs before the next slot = %d, want 1", n)
	}

	s.clock = clock.FakeClock{Time: day1.AddDate(0, 0, 1).Add(30 * time.Minute)}
	s.Tick(context.Background())
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_runs WHERE kind = 'scheduled' AND status = 'ok'`); n != 2 {
		t.Fatalf("runs after the next slot = %d, want 2", n)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read backup folder: %v", err)
	}
	var files int
	for _, e := range entries {
		if _, ok := parseName(e.Name()); ok {
			files++
		}
	}
	if files != 2 {
		t.Fatalf("archives = %d, want 2", files)
	}
}

func TestSchedulerFailureNotifiesParents(t *testing.T) {
	conn := testhelper.OpenDB(t)
	testhelper.OneFamily(t, conn)
	// A file where the backup folder should be makes the run fail.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a folder"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	s := NewScheduler(conn, clock.FakeClock{Time: time.Date(2026, 10, 21, 3, 0, 0, 0, time.Local)},
		quietLogger(), blocked, testDBKey, filepath.Join(t.TempDir(), "no-avatars"))

	s.Tick(context.Background())

	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_runs WHERE kind = 'scheduled' AND status = 'failed'`); n != 1 {
		t.Fatalf("failed runs = %d, want 1", n)
	}
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM notifications WHERE kind = 'backup_failed'`); n != 1 {
		t.Fatalf("failure notices = %d, want 1 (one parent)", n)
	}
	// The failed slot is not retried the same day.
	s.Tick(context.Background())
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_runs`); n != 1 {
		t.Fatalf("runs after a second tick = %d, want 1", n)
	}
}

func TestStaleWarningOncePerDay(t *testing.T) {
	conn := testhelper.OpenDB(t)
	testhelper.OneFamily(t, conn)
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.Local)

	// Today's slot is already recorded as failed, so no new run starts. The last good backup is four days old.
	mustInsertRun(t, conn, "failed", scheduledSlot(now, 2).Add(time.Minute), time.Time{})
	mustInsertRun(t, conn, "ok", now.AddDate(0, 0, -4), now.AddDate(0, 0, -4))

	s := newTestScheduler(conn, t.TempDir(), now)
	s.Tick(context.Background())
	s.clock = clock.FakeClock{Time: now.Add(2 * time.Hour)}
	s.Tick(context.Background())

	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM notifications WHERE kind = 'backup_stale'`); n != 1 {
		t.Fatalf("stale notices = %d, want 1 for the day", n)
	}
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_runs WHERE kind = 'scheduled'`); n != 2 {
		t.Fatalf("scheduled runs = %d, want the 2 seeded ones and no new run", n)
	}
}

func TestStaleWarningNotRaisedForRecentBackup(t *testing.T) {
	conn := testhelper.OpenDB(t)
	testhelper.OneFamily(t, conn)
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.Local)
	mustInsertRun(t, conn, "failed", scheduledSlot(now, 2).Add(time.Minute), time.Time{})
	mustInsertRun(t, conn, "ok", now.AddDate(0, 0, -1), now.AddDate(0, 0, -1))

	newTestScheduler(conn, t.TempDir(), now).Tick(context.Background())

	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM notifications WHERE kind = 'backup_stale'`); n != 0 {
		t.Fatalf("stale notices = %d, want 0", n)
	}
}
