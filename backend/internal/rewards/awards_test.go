package rewards

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/tasks"
	"nestwall/backend/internal/testhelper"
)

var testNow = time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC)

// seedOccurrence creates a one-off task definition and one occurrence for the family.
func seedOccurrence(t *testing.T, conn *sql.DB, f testhelper.Family, points int, latePercent sql.NullInt64) string {
	t.Helper()
	defID := ids.New()
	occID := ids.New()
	if _, err := conn.Exec(
		`INSERT INTO task_definitions (id, family_id, title, time_zone, starts_on, points, late_points_percent, created_by)
		 VALUES (?, ?, 'Make bed', 'UTC', '2026-10-12', ?, ?, ?)`,
		defID, f.FamilyID, points, latePercent, f.ParentUserID,
	); err != nil {
		t.Fatalf("insert definition: %v", err)
	}
	if _, err := conn.Exec(
		`INSERT INTO task_occurrences (id, family_id, task_definition_id, due_date, due_at)
		 VALUES (?, ?, ?, '2026-10-12', '2026-10-12 18:00:00.000+00:00')`,
		occID, f.FamilyID, defID,
	); err != nil {
		t.Fatalf("insert occurrence: %v", err)
	}
	return occID
}

// newAwards returns the award participant with the test clock.
func newAwards() *TaskAwards {
	return NewTaskAwards(clock.FakeClock{Time: testNow})
}

// apply runs the award in its own transaction, as the approval does, and fails the test on error.
func apply(t *testing.T, conn *sql.DB, a *TaskAwards, e tasks.Event) {
	t.Helper()
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if err := a.Apply(ctx, tx, e); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func approved(f testhelper.Family, occID string, late bool, completedAt string) tasks.Event {
	return tasks.Event{
		Type:        tasks.EventApproved,
		FamilyID:    f.FamilyID,
		TaskID:      occID,
		MemberID:    f.MemberID,
		Late:        late,
		CompletedAt: completedAt,
	}
}

func reopened(f testhelper.Family, occID string) tasks.Event {
	return tasks.Event{
		Type:     tasks.EventReopened,
		FamilyID: f.FamilyID,
		TaskID:   occID,
		MemberID: f.MemberID,
	}
}

func memberBalance(t *testing.T, conn *sql.DB, f testhelper.Family) int {
	t.Helper()
	total, err := Balance(context.Background(), conn, f.FamilyID, f.MemberID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	return total
}

func memberRows(t *testing.T, conn *sql.DB, f testhelper.Family) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM points_ledger WHERE family_id = ? AND family_member_id = ?`,
		f.FamilyID, f.MemberID,
	).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func TestAwardAmountRounding(t *testing.T) {
	pct := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
	cases := []struct {
		name   string
		points int
		pct    sql.NullInt64
		late   bool
		want   int
	}{
		{"on time gets full points", 15, pct(50), false, 15},
		{"late 50 percent of 15 is 8", 15, pct(50), true, 8},
		{"late 50 percent of 7 rounds half up to 4", 7, pct(50), true, 4},
		{"late 33 percent of 10 is 3", 10, pct(33), true, 3},
		{"late 100 percent keeps all points", 10, pct(100), true, 10},
		{"late 0 percent earns nothing", 10, pct(0), true, 0},
		{"late without a percentage earns full points", 15, sql.NullInt64{}, true, 15},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := awardAmount(c.points, c.pct, c.late); got != c.want {
				t.Fatalf("awardAmount = %d, want %d", got, c.want)
			}
		})
	}
}

func TestOnTimeApprovalAwardsPoints(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	occ := seedOccurrence(t, conn, f, 15, sql.NullInt64{Int64: 50, Valid: true})

	apply(t, conn, newAwards(), approved(f, occ, false, "2026-10-12 15:00:00.000+00:00"))

	if got := memberBalance(t, conn, f); got != 15 {
		t.Fatalf("balance = %d, want 15", got)
	}
}

func TestLateApprovalAwardsPercentageOnce(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	occ := seedOccurrence(t, conn, f, 15, sql.NullInt64{Int64: 50, Valid: true})
	ev := approved(f, occ, true, "2026-10-12 19:00:00.000+00:00")

	awards := newAwards()
	apply(t, conn, awards, ev)
	apply(t, conn, awards, ev) // a replay of the same event

	if got := memberBalance(t, conn, f); got != 8 {
		t.Fatalf("balance = %d, want 8", got)
	}
	if got := memberRows(t, conn, f); got != 1 {
		t.Fatalf("rows = %d, want 1", got)
	}
}

func TestLateWithoutPercentageAwardsFullPoints(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	occ := seedOccurrence(t, conn, f, 15, sql.NullInt64{})

	apply(t, conn, newAwards(), approved(f, occ, true, "2026-10-12 19:00:00.000+00:00"))

	if got := memberBalance(t, conn, f); got != 15 {
		t.Fatalf("balance = %d, want 15", got)
	}
}

func TestZeroPointsPostsNothing(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	occ := seedOccurrence(t, conn, f, 0, sql.NullInt64{})

	apply(t, conn, newAwards(), approved(f, occ, false, "2026-10-12 15:00:00.000+00:00"))

	if got := memberRows(t, conn, f); got != 0 {
		t.Fatalf("rows = %d, want 0", got)
	}
}

func TestReopenReversesAndReapprovalAwardsAgain(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	occ := seedOccurrence(t, conn, f, 15, sql.NullInt64{Int64: 50, Valid: true})
	awards := newAwards()

	apply(t, conn, awards, approved(f, occ, true, "2026-10-12 19:00:00.000+00:00"))
	if got := memberBalance(t, conn, f); got != 8 {
		t.Fatalf("after approval balance = %d, want 8", got)
	}

	apply(t, conn, awards, reopened(f, occ))
	apply(t, conn, awards, reopened(f, occ)) // a second reopen must not reverse twice
	if got := memberBalance(t, conn, f); got != 0 {
		t.Fatalf("after reopen balance = %d, want 0", got)
	}

	// The task is done again on time and approved. This completion has its own time, so it is awarded.
	apply(t, conn, awards, approved(f, occ, false, "2026-10-12 20:00:00.000+00:00"))
	if got := memberBalance(t, conn, f); got != 15 {
		t.Fatalf("after re-approval balance = %d, want 15", got)
	}
	if got := memberRows(t, conn, f); got != 3 {
		t.Fatalf("rows = %d, want 3 (award, reversal, award)", got)
	}
}

func TestReopenWithoutAwardPostsNothing(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	occ := seedOccurrence(t, conn, f, 15, sql.NullInt64{})

	apply(t, conn, newAwards(), reopened(f, occ))

	if got := memberRows(t, conn, f); got != 0 {
		t.Fatalf("rows = %d, want 0", got)
	}
}
