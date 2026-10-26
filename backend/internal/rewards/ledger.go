// Package rewards holds the points ledger. A balance changes only by adding a row to
// points_ledger, and rows are never updated or deleted.
package rewards

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"nestwall/backend/internal/platform/ids"
)

// dbTimeLayout matches the text format the schema stores, so text order is time order.
const dbTimeLayout = "2006-01-02 15:04:05.000"

// EntryType is the kind of a ledger row. The values match the CHECK in migration 000010.
type EntryType string

const (
	TaskAward          EntryType = "task_award"
	BonusTaskAward     EntryType = "bonus_task_award"
	Bonus              EntryType = "bonus"
	Penalty            EntryType = "penalty"
	Redemption         EntryType = "redemption"
	RedemptionReversal EntryType = "redemption_reversal"
	Reversal           EntryType = "reversal"
	Adjustment         EntryType = "adjustment"
	TransferOut        EntryType = "transfer_out"
	TransferIn         EntryType = "transfer_in"
)

// ErrInvalidEntry means the entry breaks a rule of the ledger. Nothing is written.
var ErrInvalidEntry = errors.New("rewards: invalid ledger entry")

// ErrIdempotencyConflict means the key was already used for a different entry.
var ErrIdempotencyConflict = errors.New("rewards: idempotency key was used for a different entry")

// Entry is one ledger row to post. Amount is signed: awards and grants are positive,
// penalties and spending are negative.
type Entry struct {
	FamilyID       string
	MemberID       string
	Type           EntryType
	Amount         int
	Reason         string // optional
	ReferenceType  string // set together with ReferenceID
	ReferenceID    string
	ActorMemberID  string // empty for system posts
	TransferID     string // required for transfer types only
	IdempotencyKey string
}

// Posted is the result of Post. Replayed is true when the key was already in the ledger, in
// which case nothing was written and ID is the existing row.
type Posted struct {
	ID       string
	Replayed bool
}

// Post writes one entry inside the caller's transaction. A key that is already in the ledger for
// the same family is a no-op. The same key with different content is ErrIdempotencyConflict.
func Post(ctx context.Context, tx *sql.Tx, now time.Time, e Entry) (Posted, error) {
	if err := e.validate(); err != nil {
		return Posted{}, err
	}

	id := ids.New()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO points_ledger (id, family_id, family_member_id, entry_type, amount, reason,
			reference_type, reference_id, actor_member_id, transfer_id, idempotency_key, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (family_id, idempotency_key) DO NOTHING`,
		id, e.FamilyID, e.MemberID, string(e.Type), e.Amount, nullable(e.Reason),
		nullable(e.ReferenceType), nullable(e.ReferenceID), nullable(e.ActorMemberID),
		nullable(e.TransferID), e.IdempotencyKey, dbTime(now),
	)
	if err != nil {
		return Posted{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Posted{}, err
	}
	if affected == 1 {
		return Posted{ID: id}, nil
	}
	return replay(ctx, tx, e)
}

// replay returns the row already stored under the entry's key, after checking that it matches.
func replay(ctx context.Context, tx *sql.Tx, e Entry) (Posted, error) {
	var (
		id, memberID, entryType    string
		amount                     int
		refType, refID, transferID sql.NullString
	)
	err := tx.QueryRowContext(ctx,
		`SELECT id, family_member_id, entry_type, amount, reference_type, reference_id, transfer_id
		 FROM points_ledger WHERE family_id = ? AND idempotency_key = ?`,
		e.FamilyID, e.IdempotencyKey,
	).Scan(&id, &memberID, &entryType, &amount, &refType, &refID, &transferID)
	if err != nil {
		return Posted{}, err
	}
	if memberID != e.MemberID || entryType != string(e.Type) || amount != e.Amount ||
		refType.String != e.ReferenceType || refID.String != e.ReferenceID || transferID.String != e.TransferID {
		return Posted{}, ErrIdempotencyConflict
	}
	return Posted{ID: id, Replayed: true}, nil
}

// Querier is satisfied by *sql.DB and *sql.Tx.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Balance returns the member's points: the sum of their ledger entries. A pending redemption is
// a negative redemption entry, so it already reduces the balance.
func Balance(ctx context.Context, q Querier, familyID, memberID string) (int, error) {
	var total int
	err := q.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM points_ledger WHERE family_id = ? AND family_member_id = ?`,
		familyID, memberID,
	).Scan(&total)
	return total, err
}

// validate applies the ledger rules that the schema also checks, so callers get a clear error.
func (e Entry) validate() error {
	if e.FamilyID == "" || e.MemberID == "" || e.IdempotencyKey == "" {
		return fmt.Errorf("%w: family, member and idempotency key are required", ErrInvalidEntry)
	}
	if (e.ReferenceType == "") != (e.ReferenceID == "") {
		return fmt.Errorf("%w: reference type and reference id go together", ErrInvalidEntry)
	}
	isTransfer := e.Type == TransferOut || e.Type == TransferIn
	if isTransfer != (e.TransferID != "") {
		return fmt.Errorf("%w: a transfer id is required for transfers and not allowed otherwise", ErrInvalidEntry)
	}
	if !signAllowed(e.Type, e.Amount) {
		return fmt.Errorf("%w: amount %d does not fit entry type %q", ErrInvalidEntry, e.Amount, e.Type)
	}
	return nil
}

// signAllowed reports whether amount has the sign the entry type requires.
func signAllowed(t EntryType, amount int) bool {
	switch t {
	case TaskAward, BonusTaskAward, Bonus, RedemptionReversal, TransferIn:
		return amount > 0
	case Penalty, Redemption, TransferOut:
		return amount < 0
	case Reversal, Adjustment:
		return amount != 0
	}
	return false
}

// nullable maps an empty string to SQL NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// dbTime formats a UTC instant the way the schema stores timestamps.
func dbTime(t time.Time) string {
	return t.UTC().Format(dbTimeLayout) + "+00:00"
}
