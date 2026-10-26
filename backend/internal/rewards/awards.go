package rewards

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/tasks"
)

// refTaskOccurrence is the reference type for points that come from a task occurrence.
const refTaskOccurrence = "task_occurrence"

// TaskAwards posts points when a task is approved and reverses them when it is reopened. It is a
// tasks.Participant: it writes in the approval's transaction, so a points failure rolls the approval
// back instead of losing the points.
type TaskAwards struct {
	clock clock.Clock
}

// NewTaskAwards returns the participant. Register Apply with tasks.Handlers.Participate at startup.
func NewTaskAwards(clk clock.Clock) *TaskAwards {
	return &TaskAwards{clock: clk}
}

// Apply posts or reverses points for one task event inside the caller's transaction. A reopen
// reverses the award and undoes the expiry penalty for the member. The caller commits. Other event
// types are ignored.
func (a *TaskAwards) Apply(ctx context.Context, tx *sql.Tx, e tasks.Event) error {
	switch e.Type {
	case tasks.EventApproved:
		return a.award(ctx, tx, e)
	case tasks.EventReopened:
		if err := a.reverse(ctx, tx, e); err != nil {
			return err
		}
		return undoExpiryPenalty(ctx, tx, a.clock.Now(), e.FamilyID, e.MemberID, e.TaskID)
	}
	return nil
}

// award posts task_award for one approved completion. The key holds the completion time, so
// one completion is awarded once, and a completion made again after a reopen is a new award.
func (a *TaskAwards) award(ctx context.Context, tx *sql.Tx, e tasks.Event) error {
	if e.CompletedAt == "" {
		return errors.New("approval event has no completion time")
	}

	var points int
	var latePercent sql.NullInt64
	err := tx.QueryRowContext(ctx,
		`SELECT d.points, d.late_points_percent
		 FROM task_occurrences o
		 JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
		 WHERE o.family_id = ? AND o.id = ?`,
		e.FamilyID, e.TaskID,
	).Scan(&points, &latePercent)
	if err != nil {
		return fmt.Errorf("read task points: %w", err)
	}

	amount := awardAmount(points, latePercent, e.Late)
	if amount == 0 {
		return nil
	}
	_, err = Post(ctx, tx, a.clock.Now(), Entry{
		FamilyID:       e.FamilyID,
		MemberID:       e.MemberID,
		Type:           TaskAward,
		Amount:         amount,
		ReferenceType:  refTaskOccurrence,
		ReferenceID:    e.TaskID,
		IdempotencyKey: "task_award:" + e.TaskID + ":" + e.MemberID + ":" + e.CompletedAt,
	})
	if err != nil {
		return fmt.Errorf("post award: %w", err)
	}
	return nil
}

// reverse posts a reversal of the member's latest task award for the occurrence. A reopen
// that has nothing to reverse, or whose award was already reversed, writes nothing.
func (a *TaskAwards) reverse(ctx context.Context, tx *sql.Tx, e tasks.Event) error {
	var awardKey string
	var amount int
	err := tx.QueryRowContext(ctx,
		`SELECT idempotency_key, amount FROM points_ledger
		 WHERE family_id = ? AND family_member_id = ? AND entry_type = ?
		   AND reference_type = ? AND reference_id = ?
		 ORDER BY rowid DESC LIMIT 1`,
		e.FamilyID, e.MemberID, string(TaskAward), refTaskOccurrence, e.TaskID,
	).Scan(&awardKey, &amount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find award: %w", err)
	}

	_, err = Post(ctx, tx, a.clock.Now(), Entry{
		FamilyID:       e.FamilyID,
		MemberID:       e.MemberID,
		Type:           Reversal,
		Amount:         -amount,
		ReferenceType:  refTaskOccurrence,
		ReferenceID:    e.TaskID,
		IdempotencyKey: "reversal:" + awardKey,
	})
	if err != nil {
		return fmt.Errorf("post reversal: %w", err)
	}
	return nil
}

// awardAmount returns the points for a completion. A late completion earns the late-points
// percentage of the points, rounded half up. A task without a late percentage earns full points.
func awardAmount(points int, latePercent sql.NullInt64, late bool) int {
	if !late || !latePercent.Valid {
		return points
	}
	return (points*int(latePercent.Int64) + 50) / 100
}
