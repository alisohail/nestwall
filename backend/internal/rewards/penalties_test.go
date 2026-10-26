package rewards

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/tasks"
	"nestwall/backend/internal/testhelper"
)

// penaltyDB opens a database with one family.
func penaltyDB(t *testing.T) (*sql.DB, testhelper.Family) {
	t.Helper()
	conn := testhelper.OpenDB(t)
	return conn, testhelper.OneFamily(t, conn)
}

// manualChange builds a parent's change for the family's parent member.
func manualChange(f testhelper.Family, kind EntryType, points int, key string) Manual {
	return Manual{
		FamilyID:       f.FamilyID,
		MemberID:       f.MemberID,
		ActorUserID:    f.ParentUserID,
		ActorMemberID:  f.MemberID,
		Type:           kind,
		Points:         points,
		Reason:         "test",
		IdempotencyKey: key,
	}
}

// grant posts a bonus and fails the test on error.
func grant(t *testing.T, conn *sql.DB, f testhelper.Family, points int, key string) ManualResult {
	t.Helper()
	res, err := PostManual(context.Background(), conn, testNow, manualChange(f, Bonus, points, key))
	if err != nil {
		t.Fatalf("grant %d: %v", points, err)
	}
	return res
}

// setAllowNegative writes the family's negative-balance setting.
func setAllowNegative(t *testing.T, conn *sql.DB, f testhelper.Family, value string) {
	t.Helper()
	if _, err := conn.Exec(
		`INSERT INTO app_settings (family_id, setting_key, setting_value) VALUES (?, 'points.allow_negative_balance', ?)`,
		f.FamilyID, value,
	); err != nil {
		t.Fatalf("set allow_negative_balance: %v", err)
	}
}

// countEntries returns how many ledger rows of a type the member has.
func countEntries(t *testing.T, conn *sql.DB, f testhelper.Family, kind EntryType) string {
	t.Helper()
	var n string
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM points_ledger WHERE family_id = ? AND family_member_id = ? AND entry_type = ?`,
		f.FamilyID, f.MemberID, string(kind),
	).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", kind, err)
	}
	return n
}

func TestPenaltyIsCappedAtBalanceAndRecordsCappedAmount(t *testing.T) {
	conn, f := penaltyDB(t)
	grant(t, conn, f, 10, "g1")

	res, err := PostManual(context.Background(), conn, testNow, manualChange(f, Penalty, 25, "p1"))
	if err != nil {
		t.Fatalf("penalty: %v", err)
	}
	if res.Amount != -10 || res.Balance != 0 {
		t.Fatalf("amount = %d, balance = %d; want -10 and 0", res.Amount, res.Balance)
	}
	if got := memberBalance(t, conn, f); got != 0 {
		t.Fatalf("ledger balance = %d, want 0", got)
	}
}

func TestPenaltyTakesBelowZeroWhenSettingIsOn(t *testing.T) {
	conn, f := penaltyDB(t)
	setAllowNegative(t, conn, f, "true")
	grant(t, conn, f, 10, "g1")

	res, err := PostManual(context.Background(), conn, testNow, manualChange(f, Penalty, 25, "p1"))
	if err != nil {
		t.Fatalf("penalty: %v", err)
	}
	if res.Amount != -25 || res.Balance != -15 {
		t.Fatalf("amount = %d, balance = %d; want -25 and -15", res.Amount, res.Balance)
	}
}

func TestPenaltyAtZeroBalanceIsRefusedAndWritesNothing(t *testing.T) {
	conn, f := penaltyDB(t)

	_, err := PostManual(context.Background(), conn, testNow, manualChange(f, Penalty, 5, "p1"))
	if err != ErrNothingToPenalise {
		t.Fatalf("err = %v, want ErrNothingToPenalise", err)
	}
	if got := countEntries(t, conn, f, Penalty); got != "0" {
		t.Fatalf("penalty rows = %s, want 0", got)
	}
}

func TestManualReplayReturnsStoredRowAndConflictsOnOtherType(t *testing.T) {
	conn, f := penaltyDB(t)
	first := grant(t, conn, f, 10, "g1")

	again := grant(t, conn, f, 10, "g1")
	if !again.Replayed || again.EntryID != first.EntryID {
		t.Fatalf("replay = %+v, want replayed row %s", again, first.EntryID)
	}
	if got := memberBalance(t, conn, f); got != 10 {
		t.Fatalf("balance after replay = %d, want 10", got)
	}

	_, err := PostManual(context.Background(), conn, testNow, manualChange(f, Penalty, 5, "g1"))
	if err != ErrIdempotencyConflict {
		t.Fatalf("reused key err = %v, want ErrIdempotencyConflict", err)
	}
}

func TestManualRejectsMemberOutsideFamily(t *testing.T) {
	conn, f := penaltyDB(t)
	m := manualChange(f, Bonus, 5, "g1")
	m.MemberID = "no-such-member"

	if _, err := PostManual(context.Background(), conn, testNow, m); err != ErrMemberNotFound {
		t.Fatalf("err = %v, want ErrMemberNotFound", err)
	}
}

func TestReversePenaltyOnceAndOnlyPenalties(t *testing.T) {
	conn, f := penaltyDB(t)
	bonus := grant(t, conn, f, 20, "g1")
	pen, err := PostManual(context.Background(), conn, testNow, manualChange(f, Penalty, 5, "p1"))
	if err != nil {
		t.Fatalf("penalty: %v", err)
	}
	ctx := context.Background()

	rev, err := ReversePenalty(ctx, conn, testNow, f.FamilyID, pen.EntryID, f.ParentUserID, f.MemberID, "Mistake")
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if rev.Amount != 5 || rev.Balance != 20 {
		t.Fatalf("reversal amount = %d, balance = %d; want 5 and 20", rev.Amount, rev.Balance)
	}

	if _, err := ReversePenalty(ctx, conn, testNow, f.FamilyID, pen.EntryID, f.ParentUserID, f.MemberID, "Again"); err != ErrAlreadyReversed {
		t.Fatalf("second reverse err = %v, want ErrAlreadyReversed", err)
	}
	if _, err := ReversePenalty(ctx, conn, testNow, f.FamilyID, bonus.EntryID, f.ParentUserID, f.MemberID, "Bonus"); err != ErrNotPenalty {
		t.Fatalf("reverse bonus err = %v, want ErrNotPenalty", err)
	}
	if _, err := ReversePenalty(ctx, conn, testNow, f.FamilyID, "missing", f.ParentUserID, f.MemberID, "Missing"); err != ErrEntryNotFound {
		t.Fatalf("reverse missing err = %v, want ErrEntryNotFound", err)
	}
	if got := memberBalance(t, conn, f); got != 20 {
		t.Fatalf("balance = %d, want 20", got)
	}
}

// penaltyDefinition inserts a daily task due at 08:00 UTC with a 120-minute grace, a 3-point
// penalty, and the family's parent as the assignee. The occurrence is missed at 10:00 UTC.
func penaltyDefinition(t *testing.T, conn *sql.DB, f testhelper.Family) {
	t.Helper()
	defID := ids.New()
	if _, err := conn.Exec(
		`INSERT INTO task_definitions (id, family_id, title, time_zone, starts_on, due_time, recurrence_rule,
			points, expires_after_minutes, penalty_value, penalty_unit, created_by)
		 VALUES (?, ?, 'Tidy room', 'UTC', '2026-10-10', '08:00', 'FREQ=DAILY', 1, 120, 3, 'points', ?)`,
		defID, f.FamilyID, f.ParentUserID,
	); err != nil {
		t.Fatalf("insert definition: %v", err)
	}
	if _, err := conn.Exec(
		`INSERT INTO task_definition_assignees (id, family_id, task_definition_id, family_member_id) VALUES (?, ?, ?, ?)`,
		ids.New(), f.FamilyID, defID, f.MemberID,
	); err != nil {
		t.Fatalf("insert assignee: %v", err)
	}
}

// runMissed runs the schedule at 10:00 UTC, when the occurrence is missed and its penalty intent
// is created.
func runMissed(t *testing.T, conn *sql.DB) {
	t.Helper()
	at, err := time.Parse(time.RFC3339, "2026-10-10T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.RunSchedule(context.Background(), conn, at); err != nil {
		t.Fatalf("RunSchedule: %v", err)
	}
}

func TestMissedTaskPenaltyIsPostedOnceAndSettled(t *testing.T) {
	conn, f := penaltyDB(t)
	grant(t, conn, f, 10, "g1")
	penaltyDefinition(t, conn, f)
	runMissed(t, conn)

	ctx := context.Background()
	if n, err := SettlePenalties(ctx, conn, testNow); err != nil || n != 1 {
		t.Fatalf("first settle = %d, %v; want 1, nil", n, err)
	}
	if n, err := SettlePenalties(ctx, conn, testNow); err != nil || n != 0 {
		t.Fatalf("second settle = %d, %v; want 0, nil", n, err)
	}
	if got := countEntries(t, conn, f, Penalty); got != "1" {
		t.Fatalf("penalty rows = %s, want 1", got)
	}
	if got := memberBalance(t, conn, f); got != 7 {
		t.Fatalf("balance = %d, want 7 (10 minus 3)", got)
	}
	var reason string
	if err := conn.QueryRow(
		`SELECT reason FROM points_ledger WHERE family_id = ? AND entry_type = 'penalty'`, f.FamilyID,
	).Scan(&reason); err != nil || reason != missedReason {
		t.Fatalf("reason = %q, %v; want %q", reason, err, missedReason)
	}
}

func TestMissedTaskPenaltyAtZeroBalanceIsSettledWithoutRow(t *testing.T) {
	conn, f := penaltyDB(t)
	penaltyDefinition(t, conn, f)
	runMissed(t, conn)

	ctx := context.Background()
	if n, err := SettlePenalties(ctx, conn, testNow); err != nil || n != 1 {
		t.Fatalf("settle at zero balance = %d, %v; want 1, nil", n, err)
	}
	if got := countEntries(t, conn, f, Penalty); got != "0" {
		t.Fatalf("penalty rows = %s, want 0", got)
	}

	// Points arrive later. The settled intent must not be charged afterwards.
	grant(t, conn, f, 10, "g1")
	if n, err := SettlePenalties(ctx, conn, testNow); err != nil || n != 0 {
		t.Fatalf("settle after top-up = %d, %v; want 0, nil", n, err)
	}
	if got := memberBalance(t, conn, f); got != 10 {
		t.Fatalf("balance = %d, want 10", got)
	}
}
