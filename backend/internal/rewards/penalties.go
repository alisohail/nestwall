package rewards

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"nestwall/backend/internal/audit"
)

// settingAllowNegative is the app_settings key that lets a penalty take the balance below zero.
const settingAllowNegative = "points.allow_negative_balance"

// missedReason is the ledger reason for a penalty that the scheduler posts for a missed task.
const missedReason = "Missed: task"

// settleBatch is the most penalty intents one scheduler pass settles.
const settleBatch = 500

// Errors returned by the penalty and manual points functions.
var (
	// ErrNothingToPenalise means the penalty would take no points: the balance is zero or below
	// and negative balances are off. Nothing is written.
	ErrNothingToPenalise = errors.New("rewards: no balance to penalise")
	// ErrMemberNotFound means the member is not in the caller's family.
	ErrMemberNotFound = errors.New("rewards: member not found")
	// ErrEntryNotFound means the ledger entry is not in the caller's family.
	ErrEntryNotFound = errors.New("rewards: ledger entry not found")
	// ErrNotPenalty means the entry to reverse is not a penalty.
	ErrNotPenalty = errors.New("rewards: entry is not a penalty")
	// ErrAlreadyReversed means the penalty was already reversed.
	ErrAlreadyReversed = errors.New("rewards: penalty already reversed")
)

// allowNegativeBalance reports whether the family lets penalties take the balance below zero.
// A missing setting means off.
func allowNegativeBalance(ctx context.Context, q Querier, familyID string) (bool, error) {
	var value string
	err := q.QueryRowContext(ctx,
		`SELECT setting_value FROM app_settings WHERE family_id = ? AND setting_key = ?`,
		familyID, settingAllowNegative,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// capPenalty returns how many points a penalty of points can take from the member. With negative
// balances off, the penalty is capped at the available balance, which can be zero.
func capPenalty(ctx context.Context, tx *sql.Tx, familyID, memberID string, points int) (int, error) {
	allowNeg, err := allowNegativeBalance(ctx, tx, familyID)
	if err != nil {
		return 0, fmt.Errorf("read negative balance setting: %w", err)
	}
	if allowNeg {
		return points, nil
	}
	balance, err := Balance(ctx, tx, familyID, memberID)
	if err != nil {
		return 0, fmt.Errorf("read balance: %w", err)
	}
	if balance <= 0 {
		return 0, nil
	}
	if points > balance {
		return balance, nil
	}
	return points, nil
}

// penalty describes one penalty to post. Points is the requested size and is positive.
type penalty struct {
	FamilyID       string
	MemberID       string
	Points         int
	Reason         string
	ReferenceType  string
	ReferenceID    string
	ActorMemberID  string
	IdempotencyKey string
}

// postPenalty writes a penalty capped at the available balance inside the caller's transaction.
// It returns the points taken and the new ledger row ID. When the cap leaves nothing, it writes
// no row and returns zero points with an empty ID.
func postPenalty(ctx context.Context, tx *sql.Tx, now time.Time, p penalty) (int, string, error) {
	applied, err := capPenalty(ctx, tx, p.FamilyID, p.MemberID, p.Points)
	if err != nil {
		return 0, "", err
	}
	if applied == 0 {
		return 0, "", nil
	}
	posted, err := Post(ctx, tx, now, Entry{
		FamilyID:       p.FamilyID,
		MemberID:       p.MemberID,
		Type:           Penalty,
		Amount:         -applied,
		Reason:         p.Reason,
		ReferenceType:  p.ReferenceType,
		ReferenceID:    p.ReferenceID,
		ActorMemberID:  p.ActorMemberID,
		IdempotencyKey: p.IdempotencyKey,
	})
	if err != nil {
		return 0, "", fmt.Errorf("post penalty: %w", err)
	}
	return applied, posted.ID, nil
}

// SettlePenalties posts the penalty for each unsettled task penalty intent and marks the intent
// settled, in the same transaction. A settled intent is never posted again, so each missed
// occurrence is penalised once, even when the balance was zero at the time. An intent cancelled by
// a reopen is never posted. It returns how many intents it settled. A failed intent stops the pass
// and is retried on the next run.
func SettlePenalties(ctx context.Context, db *sql.DB, now time.Time) (int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, family_id, task_occurrence_id, family_member_id, penalty_points
		 FROM task_penalty_intents WHERE settled_at IS NULL AND cancelled_at IS NULL
		 ORDER BY created_at, id LIMIT ?`, settleBatch)
	if err != nil {
		return 0, fmt.Errorf("list penalty intents: %w", err)
	}
	type intent struct {
		id, familyID, occurrenceID, memberID string
		points                               int
	}
	var list []intent
	for rows.Next() {
		var in intent
		if err := rows.Scan(&in.id, &in.familyID, &in.occurrenceID, &in.memberID, &in.points); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan penalty intent: %w", err)
		}
		list = append(list, in)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("read penalty intents: %w", err)
	}
	rows.Close()

	settled := 0
	for _, in := range list {
		done, err := settleIntent(ctx, db, now, in.id, penalty{
			FamilyID:       in.familyID,
			MemberID:       in.memberID,
			Points:         in.points,
			Reason:         missedReason,
			ReferenceType:  refTaskOccurrence,
			ReferenceID:    in.occurrenceID,
			IdempotencyKey: "penalty:" + in.id,
		})
		if err != nil {
			return settled, err
		}
		if done {
			settled++
		}
	}
	return settled, nil
}

// settleIntent marks one intent settled and posts its penalty in one transaction. The intent is
// marked first, under the write lock, so an intent cancelled after it was listed is skipped. It
// returns false when there was nothing to settle.
func settleIntent(ctx context.Context, db *sql.DB, now time.Time, intentID string, p penalty) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin settlement: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE task_penalty_intents SET settled_at = ?
		 WHERE id = ? AND settled_at IS NULL AND cancelled_at IS NULL`,
		dbTime(now), intentID,
	)
	if err != nil {
		return false, fmt.Errorf("mark intent settled: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mark intent settled: %w", err)
	}
	if n == 0 {
		return false, nil
	}

	if _, _, err := postPenalty(ctx, tx, now, p); err != nil {
		return false, fmt.Errorf("settle intent %s: %w", intentID, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit settlement: %w", err)
	}
	return true, nil
}

// reopenedReason is the ledger reason for a penalty reversed because its task was reopened.
const reopenedReason = "Reopened: task"

// undoExpiryPenalty undoes the expiry penalty of one member's copy when its occurrence is reopened.
// It runs in the caller's transaction. An unsettled intent is cancelled, so the scheduler never
// posts it. A penalty already posted is reversed once. The reversal is keyed on the penalty's
// ledger row, the same key ReversePenalty uses, so a manual reversal and this one cannot both
// refund it. A reopened occurrence is not penalised again when it is missed a second time, because
// the intent row for the member and occurrence still exists.
func undoExpiryPenalty(ctx context.Context, tx *sql.Tx, now time.Time, familyID, memberID, occurrenceID string) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE task_penalty_intents SET cancelled_at = ?
		 WHERE family_id = ? AND task_occurrence_id = ? AND family_member_id = ?
		   AND settled_at IS NULL AND cancelled_at IS NULL`,
		dbTime(now), familyID, occurrenceID, memberID,
	); err != nil {
		return fmt.Errorf("cancel penalty intent: %w", err)
	}

	type posted struct {
		id     string
		amount int
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id, amount FROM points_ledger
		 WHERE family_id = ? AND family_member_id = ? AND entry_type = ?
		   AND reference_type = ? AND reference_id = ?`,
		familyID, memberID, string(Penalty), refTaskOccurrence, occurrenceID,
	)
	if err != nil {
		return fmt.Errorf("find penalties: %w", err)
	}
	var list []posted
	for rows.Next() {
		var p posted
		if err := rows.Scan(&p.id, &p.amount); err != nil {
			rows.Close()
			return fmt.Errorf("scan penalty: %w", err)
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read penalties: %w", err)
	}
	rows.Close()

	for _, p := range list {
		if _, err := Post(ctx, tx, now, Entry{
			FamilyID:       familyID,
			MemberID:       memberID,
			Type:           Reversal,
			Amount:         -p.amount,
			Reason:         reopenedReason,
			ReferenceType:  "points_ledger",
			ReferenceID:    p.id,
			IdempotencyKey: "reversal:" + p.id,
		}); err != nil {
			return fmt.Errorf("reverse penalty %s: %w", p.id, err)
		}
	}
	return nil
}

// Manual describes a parent's bonus, penalty or adjustment. Points is positive for a bonus and a
// penalty, and signed and non-zero for an adjustment.
type Manual struct {
	FamilyID       string
	MemberID       string
	ActorUserID    string
	ActorMemberID  string
	Type           EntryType
	Points         int
	Reason         string
	IdempotencyKey string
}

// ManualResult is the outcome of a manual change. Replayed is true when the key was already used,
// in which case nothing new was written.
type ManualResult struct {
	EntryID  string
	Type     EntryType
	Amount   int
	Balance  int
	Replayed bool
}

// manualKey is the ledger key for a manual change. It is kept apart from scheduler keys.
func manualKey(key string) string {
	return "manual:" + key
}

// PostManual writes a bonus, penalty or adjustment for a member in one transaction. It is
// idempotent on the key: a repeat with the same key and type returns the stored row. A key reused
// for another member or type is ErrIdempotencyConflict.
func PostManual(ctx context.Context, db *sql.DB, now time.Time, m Manual) (ManualResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ManualResult{}, fmt.Errorf("begin manual change: %w", err)
	}
	defer tx.Rollback()

	var one int
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM family_members WHERE family_id = ? AND id = ?`, m.FamilyID, m.MemberID,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ManualResult{}, ErrMemberNotFound
	}
	if err != nil {
		return ManualResult{}, fmt.Errorf("check member: %w", err)
	}

	key := manualKey(m.IdempotencyKey)
	if stored, found, err := findManual(ctx, tx, m.FamilyID, key); err != nil {
		return ManualResult{}, err
	} else if found {
		if stored.memberID != m.MemberID || stored.entryType != string(m.Type) {
			return ManualResult{}, ErrIdempotencyConflict
		}
		balance, err := Balance(ctx, tx, m.FamilyID, m.MemberID)
		if err != nil {
			return ManualResult{}, fmt.Errorf("read balance: %w", err)
		}
		return ManualResult{EntryID: stored.id, Type: m.Type, Amount: stored.amount, Balance: balance, Replayed: true}, nil
	}

	result := ManualResult{Type: m.Type}
	switch m.Type {
	case Bonus, Adjustment:
		posted, err := Post(ctx, tx, now, Entry{
			FamilyID:       m.FamilyID,
			MemberID:       m.MemberID,
			Type:           m.Type,
			Amount:         m.Points,
			Reason:         m.Reason,
			ActorMemberID:  m.ActorMemberID,
			IdempotencyKey: key,
		})
		if err != nil {
			return ManualResult{}, fmt.Errorf("post %s: %w", m.Type, err)
		}
		result.EntryID = posted.ID
		result.Amount = m.Points
	case Penalty:
		applied, id, err := postPenalty(ctx, tx, now, penalty{
			FamilyID:       m.FamilyID,
			MemberID:       m.MemberID,
			Points:         m.Points,
			Reason:         m.Reason,
			ActorMemberID:  m.ActorMemberID,
			IdempotencyKey: key,
		})
		if err != nil {
			return ManualResult{}, err
		}
		if applied == 0 {
			return ManualResult{}, ErrNothingToPenalise
		}
		result.EntryID = id
		result.Amount = -applied
	default:
		return ManualResult{}, fmt.Errorf("rewards: unsupported manual type %q", m.Type)
	}

	balance, err := Balance(ctx, tx, m.FamilyID, m.MemberID)
	if err != nil {
		return ManualResult{}, fmt.Errorf("read balance: %w", err)
	}
	result.Balance = balance

	if err := audit.Record(ctx, tx, m.FamilyID, m.ActorUserID, "points."+string(m.Type), "family_member", &m.MemberID,
		map[string]any{"entry_id": result.EntryID, "amount": result.Amount, "reason": m.Reason}); err != nil {
		return ManualResult{}, fmt.Errorf("audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ManualResult{}, fmt.Errorf("commit manual change: %w", err)
	}
	return result, nil
}

// storedManual is a ledger row found by key.
type storedManual struct {
	id, memberID, entryType string
	amount                  int
}

// findManual returns the ledger row stored under a key, if any.
func findManual(ctx context.Context, tx *sql.Tx, familyID, key string) (storedManual, bool, error) {
	var s storedManual
	err := tx.QueryRowContext(ctx,
		`SELECT id, family_member_id, entry_type, amount FROM points_ledger
		 WHERE family_id = ? AND idempotency_key = ?`,
		familyID, key,
	).Scan(&s.id, &s.memberID, &s.entryType, &s.amount)
	if errors.Is(err, sql.ErrNoRows) {
		return storedManual{}, false, nil
	}
	if err != nil {
		return storedManual{}, false, fmt.Errorf("find stored entry: %w", err)
	}
	return s, true, nil
}

// ReversePenalty posts a compensating entry that undoes a penalty. The reversal is keyed on the
// penalty row, so a penalty can be reversed once: a second call returns ErrAlreadyReversed.
func ReversePenalty(ctx context.Context, db *sql.DB, now time.Time, familyID, entryID, actorUserID, actorMemberID, reason string) (ManualResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ManualResult{}, fmt.Errorf("begin reversal: %w", err)
	}
	defer tx.Rollback()

	var memberID, entryType string
	var amount int
	err = tx.QueryRowContext(ctx,
		`SELECT family_member_id, entry_type, amount FROM points_ledger WHERE family_id = ? AND id = ?`,
		familyID, entryID,
	).Scan(&memberID, &entryType, &amount)
	if errors.Is(err, sql.ErrNoRows) {
		return ManualResult{}, ErrEntryNotFound
	}
	if err != nil {
		return ManualResult{}, fmt.Errorf("find entry: %w", err)
	}
	if entryType != string(Penalty) {
		return ManualResult{}, ErrNotPenalty
	}

	posted, err := Post(ctx, tx, now, Entry{
		FamilyID:       familyID,
		MemberID:       memberID,
		Type:           Reversal,
		Amount:         -amount,
		Reason:         reason,
		ReferenceType:  "points_ledger",
		ReferenceID:    entryID,
		ActorMemberID:  actorMemberID,
		IdempotencyKey: "reversal:" + entryID,
	})
	if err != nil {
		return ManualResult{}, fmt.Errorf("post reversal: %w", err)
	}
	if posted.Replayed {
		return ManualResult{}, ErrAlreadyReversed
	}

	balance, err := Balance(ctx, tx, familyID, memberID)
	if err != nil {
		return ManualResult{}, fmt.Errorf("read balance: %w", err)
	}
	if err := audit.Record(ctx, tx, familyID, actorUserID, "points.penalty_reversed", "points_ledger", &entryID,
		map[string]any{"reversal_id": posted.ID, "amount": -amount, "reason": reason}); err != nil {
		return ManualResult{}, fmt.Errorf("audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ManualResult{}, fmt.Errorf("commit reversal: %w", err)
	}
	return ManualResult{EntryID: posted.ID, Type: Reversal, Amount: -amount, Balance: balance}, nil
}
