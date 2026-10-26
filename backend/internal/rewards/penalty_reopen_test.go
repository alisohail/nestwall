package rewards

import (
	"context"
	"database/sql"
	"testing"

	"nestwall/backend/internal/tasks"
	"nestwall/backend/internal/testhelper"
)

// missedOccurrence returns the occurrence of the penalty intent that runMissed recorded.
func missedOccurrence(t *testing.T, conn *sql.DB) string {
	t.Helper()
	var id string
	if err := conn.QueryRow(`SELECT task_occurrence_id FROM task_penalty_intents`).Scan(&id); err != nil {
		t.Fatalf("find penalty intent: %v", err)
	}
	return id
}

// reopenEvent is the event a reopen of the member's copy emits.
func reopenEvent(f testhelper.Family, occurrenceID string) tasks.Event {
	return tasks.Event{
		Type:     tasks.EventReopened,
		FamilyID: f.FamilyID,
		TaskID:   occurrenceID,
		MemberID: f.MemberID,
		ActorID:  f.ParentUserID,
		Missed:   true,
	}
}

// penaltyEntryID returns the ID of the member's posted penalty row.
func penaltyEntryID(t *testing.T, conn *sql.DB, f testhelper.Family) string {
	t.Helper()
	var id string
	if err := conn.QueryRow(
		`SELECT id FROM points_ledger WHERE family_id = ? AND entry_type = 'penalty'`, f.FamilyID,
	).Scan(&id); err != nil {
		t.Fatalf("find penalty row: %v", err)
	}
	return id
}

func TestReopenReversesSettledPenaltyOnce(t *testing.T) {
	conn, f := penaltyDB(t)
	grant(t, conn, f, 10, "g1")
	penaltyDefinition(t, conn, f)
	runMissed(t, conn)

	ctx := context.Background()
	if n, err := SettlePenalties(ctx, conn, testNow); err != nil || n != 1 {
		t.Fatalf("settle = %d, %v; want 1, nil", n, err)
	}
	if got := memberBalance(t, conn, f); got != 7 {
		t.Fatalf("balance before reopen = %d, want 7", got)
	}

	a := newAwards()
	e := reopenEvent(f, missedOccurrence(t, conn))
	apply(t, conn, a, e)
	if got := memberBalance(t, conn, f); got != 10 {
		t.Fatalf("balance after reopen = %d, want 10", got)
	}

	apply(t, conn, a, e)
	if got := memberBalance(t, conn, f); got != 10 {
		t.Fatalf("balance after second reopen = %d, want 10", got)
	}
	if got := countEntries(t, conn, f, Reversal); got != "1" {
		t.Fatalf("reversal rows = %s, want 1", got)
	}

	if _, err := ReversePenalty(ctx, conn, testNow, f.FamilyID, penaltyEntryID(t, conn, f), f.ParentUserID, f.MemberID, "Again"); err != ErrAlreadyReversed {
		t.Fatalf("manual reverse after reopen err = %v, want ErrAlreadyReversed", err)
	}
	if n, err := SettlePenalties(ctx, conn, testNow); err != nil || n != 0 {
		t.Fatalf("settle after reopen = %d, %v; want 0, nil", n, err)
	}
}

func TestReopenCancelsUnsettledPenalty(t *testing.T) {
	conn, f := penaltyDB(t)
	grant(t, conn, f, 10, "g1")
	penaltyDefinition(t, conn, f)
	runMissed(t, conn)

	apply(t, conn, newAwards(), reopenEvent(f, missedOccurrence(t, conn)))

	ctx := context.Background()
	if n, err := SettlePenalties(ctx, conn, testNow); err != nil || n != 0 {
		t.Fatalf("settle after reopen = %d, %v; want 0, nil", n, err)
	}
	if got := countEntries(t, conn, f, Penalty); got != "0" {
		t.Fatalf("penalty rows = %s, want 0", got)
	}
	if got := memberBalance(t, conn, f); got != 10 {
		t.Fatalf("balance = %d, want 10", got)
	}
	var cancelled string
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM task_penalty_intents WHERE cancelled_at IS NOT NULL AND settled_at IS NULL`,
	).Scan(&cancelled); err != nil || cancelled != "1" {
		t.Fatalf("cancelled intents = %s, %v; want 1, nil", cancelled, err)
	}
}

func TestReopenAfterManualReversalRefundsOnce(t *testing.T) {
	conn, f := penaltyDB(t)
	grant(t, conn, f, 10, "g1")
	penaltyDefinition(t, conn, f)
	runMissed(t, conn)

	ctx := context.Background()
	if n, err := SettlePenalties(ctx, conn, testNow); err != nil || n != 1 {
		t.Fatalf("settle = %d, %v; want 1, nil", n, err)
	}
	if _, err := ReversePenalty(ctx, conn, testNow, f.FamilyID, penaltyEntryID(t, conn, f), f.ParentUserID, f.MemberID, "Mistake"); err != nil {
		t.Fatalf("manual reverse: %v", err)
	}

	apply(t, conn, newAwards(), reopenEvent(f, missedOccurrence(t, conn)))
	if got := memberBalance(t, conn, f); got != 10 {
		t.Fatalf("balance = %d, want 10 (refunded once)", got)
	}
	if got := countEntries(t, conn, f, Reversal); got != "1" {
		t.Fatalf("reversal rows = %s, want 1", got)
	}
}

func TestReopenWithoutPenaltyWritesNothing(t *testing.T) {
	conn, f := penaltyDB(t)
	grant(t, conn, f, 10, "g1")

	apply(t, conn, newAwards(), reopenEvent(f, "no-such-occurrence"))
	if got := memberBalance(t, conn, f); got != 10 {
		t.Fatalf("balance = %d, want 10", got)
	}
	if got := countEntries(t, conn, f, Reversal); got != "0" {
		t.Fatalf("reversal rows = %s, want 0", got)
	}
}
