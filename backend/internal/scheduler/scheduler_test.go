package scheduler

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	_ "time/tzdata" // the test binary must find IANA zones on any host

	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

// quietLogger discards log output so test runs stay readable.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWorkerRunOnceAppliesTransitions(t *testing.T) {
	conn := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, conn)

	defID := ids.New()
	if _, err := conn.Exec(
		`INSERT INTO task_definitions (id, family_id, title, time_zone, starts_on, due_time, recurrence_rule, points, expires_after_minutes, created_by)
		 VALUES (?, ?, 'Daily', 'America/Toronto', '2026-10-10', '08:00', 'FREQ=DAILY', 1, 120, ?)`,
		defID, fam.FamilyID, fam.ParentUserID,
	); err != nil {
		t.Fatalf("insert definition: %v", err)
	}
	if _, err := conn.Exec(
		`INSERT INTO task_definition_assignees (id, family_id, task_definition_id, family_member_id) VALUES (?, ?, ?, ?)`,
		ids.New(), fam.FamilyID, defID, fam.MemberID,
	); err != nil {
		t.Fatalf("insert assignee: %v", err)
	}

	// 13:00 UTC on 2026-10-10 is one hour after due and before the 14:00 UTC expiry.
	at := time.Date(2026, 10, 10, 13, 0, 0, 0, time.UTC)
	worker := New(conn, clock.FakeClock{Time: at}, quietLogger(), time.Hour)

	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Generated == 0 || result.PastDue != 1 || result.Missed != 0 {
		t.Fatalf("result = %+v, want generated > 0, past due 1, missed 0", result)
	}

	var status string
	if err := conn.QueryRow(
		`SELECT status FROM task_occurrences WHERE task_definition_id = ? AND due_date = '2026-10-10'`, defID,
	).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "past_due" {
		t.Fatalf("status = %q, want past_due", status)
	}
}

// fakeLinkSyncer counts passes and fails the first one when err is set.
type fakeLinkSyncer struct {
	calls int
	err   error
}

func (f *fakeLinkSyncer) SyncDueLinks(context.Context) (int, error) {
	f.calls++
	if f.calls == 1 && f.err != nil {
		return 0, f.err
	}
	return 1, nil
}

func TestTickSyncsLinksAfterSchedule(t *testing.T) {
	conn := testhelper.OpenDB(t)
	links := &fakeLinkSyncer{err: context.DeadlineExceeded}
	worker := New(conn, clock.FakeClock{Time: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}, quietLogger(), time.Hour).WithLinkSync(links)

	worker.tick(context.Background())
	worker.tick(context.Background())

	if links.calls != 2 {
		t.Fatalf("link sync passes = %d, want 2 (a failed pass must not stop later ones)", links.calls)
	}
}

func TestTickSyncsLinksWhenScheduleFails(t *testing.T) {
	conn := testhelper.OpenDB(t)
	links := &fakeLinkSyncer{}
	worker := New(conn, clock.FakeClock{Time: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}, quietLogger(), time.Hour).WithLinkSync(links)

	if err := conn.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	worker.tick(context.Background())

	if links.calls != 1 {
		t.Fatalf("link sync passes = %d, want 1 after a failed schedule run", links.calls)
	}
}

func TestTickWithoutLinkSyncerOnlyRunsSchedule(t *testing.T) {
	conn := testhelper.OpenDB(t)
	worker := New(conn, clock.FakeClock{Time: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}, quietLogger(), time.Hour)

	worker.tick(context.Background())
}

func TestWorkerRunStopsWhenContextIsCancelled(t *testing.T) {
	conn := testhelper.OpenDB(t)
	worker := New(conn, clock.RealClock{}, quietLogger(), time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}
