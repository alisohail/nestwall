package rewards

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
)

// Redemption statuses. The values match the CHECK in migration 000014.
const (
	statusPending   = "pending"
	statusApproved  = "approved"
	statusRejected  = "rejected"
	statusCancelled = "cancelled"
)

// redemptionRef is the ledger reference type for a redemption and its reversal.
const redemptionRef = "redemption"

// redemptionCols reads the redemption columns. Timestamps are cast to text so the driver returns
// the stored format and does not parse them into time.Time.
const redemptionCols = `id, reward_id, family_member_id, status, cost,
	CAST(requested_at AS TEXT), CAST(decided_at AS TEXT), decided_by_member_id, decision_reason, version`

// Errors returned by the redemption functions. The handlers map them to HTTP responses.
var (
	ErrRedemptionNotFound     = errors.New("rewards: redemption not found")
	ErrRewardUnavailable      = errors.New("rewards: reward is not active")
	ErrOutsideWindow          = errors.New("rewards: reward is outside its window")
	ErrLimitReached           = errors.New("rewards: redemption limit reached")
	ErrNotPending             = errors.New("rewards: redemption is not pending")
	ErrOwnRedemption          = errors.New("rewards: a redemption cannot be decided by its requester")
	ErrRedemptionIdemConflict = errors.New("rewards: idempotency key was used for a different reward")
)

// InsufficientPointsError means the member's balance is below the reward's cost.
type InsufficientPointsError struct {
	Balance int
	Cost    int
}

func (e *InsufficientPointsError) Error() string {
	return fmt.Sprintf("rewards: balance %d is below cost %d", e.Balance, e.Cost)
}

// RewardRedemption is one redemption request as the API returns it.
type RewardRedemption struct {
	ID                string     `json:"id"`
	RewardID          string     `json:"reward_id"`
	MemberID          string     `json:"member_id"`
	Status            string     `json:"status"`
	Cost              int        `json:"cost"`
	RequestedAt       time.Time  `json:"requested_at"`
	DecidedAt         *time.Time `json:"decided_at"`
	DecidedByMemberID *string    `json:"decided_by_member_id"`
	DecisionReason    *string    `json:"decision_reason"`
	Version           int        `json:"version"`
}

// RedemptionResult is a redemption with the requester's balance after the change. Replayed is
// true when a redeem request matched a stored request and nothing was written.
type RedemptionResult struct {
	RewardRedemption
	Balance  int  `json:"balance"`
	Replayed bool `json:"replayed"`
}

// Redeem requests a reward for the member. The window, limit and balance checks, the redemption
// row and its ledger entry all run in one transaction. The database takes its write lock at
// BEGIN (_txlock=immediate), so two requests cannot both spend the same points.
//
// A repeated key from the same member returns the stored request. The same key with a different
// reward is ErrRedemptionIdemConflict.
func Redeem(ctx context.Context, db *sql.DB, clk clock.Clock, familyID, memberID, userID, rewardID, key string) (RedemptionResult, error) {
	now := clk.Now()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return RedemptionResult{}, err
	}
	defer tx.Rollback()

	existing, err := scanRedemption(tx.QueryRowContext(ctx,
		`SELECT `+redemptionCols+` FROM redemptions WHERE family_id = ? AND family_member_id = ? AND idempotency_key = ?`,
		familyID, memberID, key))
	if err == nil {
		if existing.RewardID != rewardID {
			return RedemptionResult{}, ErrRedemptionIdemConflict
		}
		res, err := resultOf(ctx, tx, familyID, existing.ID)
		if err != nil {
			return RedemptionResult{}, err
		}
		res.Replayed = true
		return res, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RedemptionResult{}, err
	}

	child, err := memberIsChild(ctx, tx, memberID)
	if err != nil {
		return RedemptionResult{}, err
	}
	rw, err := loadReward(ctx, tx, familyID, rewardID)
	if err != nil {
		return RedemptionResult{}, err
	}
	// A child who may not see the reward gets the same answer as for a missing one.
	if !visibleTo(rw, memberID, child) {
		return RedemptionResult{}, ErrRewardNotFound
	}
	if rw.Archived || rw.Status != "active" {
		return RedemptionResult{}, ErrRewardUnavailable
	}

	loc, err := familyLocation(ctx, tx, familyID)
	if err != nil {
		return RedemptionResult{}, err
	}
	local := now.In(loc)
	if !rw.inWindow(local) {
		return RedemptionResult{}, ErrOutsideWindow
	}

	if rw.Limit != nil {
		var used int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM redemptions
			 WHERE family_id = ? AND family_member_id = ? AND reward_id = ?
			   AND status IN ('pending', 'approved') AND requested_at >= ?`,
			familyID, memberID, rewardID, dbTime(periodStart(rw.Limit.Period, local)),
		).Scan(&used); err != nil {
			return RedemptionResult{}, err
		}
		if used >= rw.Limit.Count {
			return RedemptionResult{}, ErrLimitReached
		}
	}

	balance, err := Balance(ctx, tx, familyID, memberID)
	if err != nil {
		return RedemptionResult{}, err
	}
	if balance < rw.Cost {
		return RedemptionResult{}, &InsufficientPointsError{Balance: balance, Cost: rw.Cost}
	}

	// With no approval needed, the request is approved at once and nothing waits for a parent.
	status := statusPending
	var decidedAt any
	if !rw.ApprovalRequired {
		status = statusApproved
		decidedAt = dbTime(now)
	}

	id := ids.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO redemptions (id, family_id, reward_id, family_member_id, status, cost,
			idempotency_key, requested_at, decided_at, version, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		id, familyID, rewardID, memberID, status, rw.Cost, key, dbTime(now), decidedAt, dbTime(now),
	); err != nil {
		return RedemptionResult{}, err
	}
	if _, err := Post(ctx, tx, now, Entry{
		FamilyID:       familyID,
		MemberID:       memberID,
		Type:           Redemption,
		Amount:         -rw.Cost,
		ReferenceType:  redemptionRef,
		ReferenceID:    id,
		ActorMemberID:  memberID,
		IdempotencyKey: "redemption:" + id,
	}); err != nil {
		return RedemptionResult{}, fmt.Errorf("post redemption: %w", err)
	}
	if err := audit.Record(ctx, tx, familyID, userID, "redemption.requested", "redemption", &id,
		map[string]any{"reward_id": rewardID, "cost": rw.Cost, "status": status}); err != nil {
		return RedemptionResult{}, err
	}
	if status == statusPending {
		var name string
		if err := tx.QueryRowContext(ctx,
			`SELECT u.display_name FROM family_members m JOIN users u ON u.id = m.user_id
			  WHERE m.id = ? AND m.family_id = ?`, memberID, familyID,
		).Scan(&name); err != nil {
			return RedemptionResult{}, fmt.Errorf("load member name: %w", err)
		}
		if err := notifypolicy.NotifyParents(ctx, tx, notifypolicy.Notice{
			FamilyID:  familyID,
			SubjectID: memberID,
			Kind:      "redemption_requested",
			Body:      fmt.Sprintf("%s asked for \"%s\" and it needs your approval.", name, rw.Name),
		}, now); err != nil {
			return RedemptionResult{}, err
		}
	}

	res, err := resultOf(ctx, tx, familyID, id)
	if err != nil {
		return RedemptionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RedemptionResult{}, err
	}
	return res, nil
}

// Approve moves a pending redemption to approved. The points were already taken when it was
// requested, so the ledger does not change. The requester cannot approve their own request.
// expected is the If-Match version, or -1 to skip the check.
func Approve(ctx context.Context, db *sql.DB, clk clock.Clock, familyID, id, userID, actorMemberID string, expected int) (RedemptionResult, error) {
	return decide(ctx, db, clk, familyID, id, userID, actorMemberID, expected, decideOpts{
		status: statusApproved,
		action: "redemption.approved",
		check: func(r RewardRedemption) error {
			if r.MemberID == actorMemberID {
				return ErrOwnRedemption
			}
			return nil
		},
	})
}

// Reject moves a pending redemption to rejected and gives the points back with a
// redemption_reversal entry. The reason is required.
func Reject(ctx context.Context, db *sql.DB, clk clock.Clock, familyID, id, userID, actorMemberID, reason string, expected int) (RedemptionResult, error) {
	return decide(ctx, db, clk, familyID, id, userID, actorMemberID, expected, decideOpts{
		status: statusRejected,
		reason: reason,
		action: "redemption.rejected",
		refund: true,
		check: func(r RewardRedemption) error {
			if r.MemberID == actorMemberID {
				return ErrOwnRedemption
			}
			return nil
		},
	})
}

// Cancel lets the requester withdraw a pending redemption. It gives the points back with a
// redemption_reversal entry. Only the requester can cancel, and only while the request is
// pending; an approved request is not cancelled by the child.
func Cancel(ctx context.Context, db *sql.DB, clk clock.Clock, familyID, id, userID, actorMemberID string, expected int) (RedemptionResult, error) {
	return decide(ctx, db, clk, familyID, id, userID, actorMemberID, expected, decideOpts{
		status: statusCancelled,
		action: "redemption.cancelled",
		refund: true,
		check: func(r RewardRedemption) error {
			if r.MemberID != actorMemberID {
				return ErrRedemptionNotFound
			}
			return nil
		},
	})
}

// decideOpts sets the differences between approve, reject and cancel.
type decideOpts struct {
	status string
	reason string
	action string
	refund bool
	// check runs after the redemption is loaded. It returns the error to report.
	check func(r RewardRedemption) error
}

// decide runs one decision in a single transaction: load, check the version and the caller, check
// the status, then update, refund if needed and audit.
func decide(ctx context.Context, db *sql.DB, clk clock.Clock, familyID, id, userID, actorMemberID string, expected int, o decideOpts) (RedemptionResult, error) {
	now := clk.Now()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return RedemptionResult{}, err
	}
	defer tx.Rollback()

	r, err := loadRedemption(ctx, tx, familyID, id)
	if err != nil {
		return RedemptionResult{}, err
	}
	if expected >= 0 && expected != r.Version {
		return RedemptionResult{RewardRedemption: r}, errStaleVersion
	}
	if err := o.check(r); err != nil {
		return RedemptionResult{}, err
	}
	if r.Status != statusPending {
		return RedemptionResult{}, ErrNotPending
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE redemptions SET status = ?, decided_at = ?, decided_by_member_id = ?, decision_reason = ?,
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND family_id = ? AND version = ? AND status = 'pending'`,
		o.status, dbTime(now), actorMemberID, nullable(o.reason), dbTime(now), id, familyID, r.Version,
	); err != nil {
		return RedemptionResult{}, err
	}
	if o.refund {
		if _, err := Post(ctx, tx, now, Entry{
			FamilyID:       familyID,
			MemberID:       r.MemberID,
			Type:           RedemptionReversal,
			Amount:         r.Cost,
			ReferenceType:  redemptionRef,
			ReferenceID:    id,
			ActorMemberID:  actorMemberID,
			IdempotencyKey: "redemption-reversal:" + id,
		}); err != nil {
			return RedemptionResult{}, fmt.Errorf("post redemption reversal: %w", err)
		}
	}
	if err := audit.Record(ctx, tx, familyID, userID, o.action, "redemption", &id, map[string]any{
		"reward_id": r.RewardID,
		"member_id": r.MemberID,
		"cost":      r.Cost,
		"status":    o.status,
	}); err != nil {
		return RedemptionResult{}, err
	}
	if o.status == statusApproved || o.status == statusRejected {
		var rewardName string
		if err := tx.QueryRowContext(ctx,
			`SELECT name FROM rewards WHERE id = ? AND family_id = ?`, r.RewardID, familyID,
		).Scan(&rewardName); err != nil {
			return RedemptionResult{}, fmt.Errorf("load reward name: %w", err)
		}
		body := fmt.Sprintf("Your reward \"%s\" was approved.", rewardName)
		if o.status == statusRejected {
			body = fmt.Sprintf("Your reward \"%s\" was turned down.", rewardName)
			if o.refund {
				body += " Your points were given back."
			}
			if note := strings.TrimSpace(o.reason); note != "" {
				body += " Reason: " + note
			}
		}
		if _, err := notifypolicy.Notify(ctx, tx, notifypolicy.Notice{
			FamilyID:    familyID,
			RecipientID: r.MemberID,
			SubjectID:   r.MemberID,
			Kind:        "redemption_" + o.status,
			Body:        body,
		}, now); err != nil {
			return RedemptionResult{}, err
		}
	}

	res, err := resultOf(ctx, tx, familyID, id)
	if err != nil {
		return RedemptionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RedemptionResult{}, err
	}
	return res, nil
}

// memberIsChild reports whether the member is a child account. It runs inside a caller's
// transaction, unlike the handler helper isChild.
func memberIsChild(ctx context.Context, q catalogueDB, memberID string) (bool, error) {
	var child bool
	err := q.QueryRowContext(ctx,
		`SELECT u.is_child FROM family_members m JOIN users u ON u.id = m.user_id WHERE m.id = ?`, memberID,
	).Scan(&child)
	return child, err
}

// familyLocation returns the family's time zone. An unknown zone falls back to UTC.
func familyLocation(ctx context.Context, q catalogueDB, familyID string) (*time.Location, error) {
	var zone string
	if err := q.QueryRowContext(ctx, familyZoneSQL, familyID).Scan(&zone); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.UTC, nil
	}
	return loc, nil
}

// periodStart returns the start of the limit period that local falls in. A day starts at local
// midnight, a week starts on Monday and a month starts on its first day. A total limit counts from
// the beginning of time.
func periodStart(period string, local time.Time) time.Time {
	y, m, d := local.Date()
	switch period {
	case "day":
		return time.Date(y, m, d, 0, 0, 0, 0, local.Location())
	case "week":
		offset := (int(local.Weekday()) + 6) % 7
		return time.Date(y, m, d-offset, 0, 0, 0, 0, local.Location())
	case "month":
		return time.Date(y, m, 1, 0, 0, 0, 0, local.Location())
	}
	return time.Time{}
}

// loadRedemption reads one redemption of the family.
func loadRedemption(ctx context.Context, q catalogueDB, familyID, id string) (RewardRedemption, error) {
	r, err := scanRedemption(q.QueryRowContext(ctx,
		`SELECT `+redemptionCols+` FROM redemptions WHERE id = ? AND family_id = ?`, id, familyID))
	if errors.Is(err, sql.ErrNoRows) {
		return RewardRedemption{}, ErrRedemptionNotFound
	}
	return r, err
}

// resultOf reloads a redemption and the requester's balance inside the caller's transaction.
func resultOf(ctx context.Context, tx *sql.Tx, familyID, id string) (RedemptionResult, error) {
	r, err := loadRedemption(ctx, tx, familyID, id)
	if err != nil {
		return RedemptionResult{}, err
	}
	balance, err := Balance(ctx, tx, familyID, r.MemberID)
	if err != nil {
		return RedemptionResult{}, err
	}
	return RedemptionResult{RewardRedemption: r, Balance: balance}, nil
}

// scanRedemption reads one row in the redemptionCols order. It returns sql.ErrNoRows as is.
func scanRedemption(row *sql.Row) (RewardRedemption, error) {
	var (
		r                          RewardRedemption
		requested                  string
		decided, decidedBy, reason sql.NullString
	)
	if err := row.Scan(&r.ID, &r.RewardID, &r.MemberID, &r.Status, &r.Cost, &requested,
		&decided, &decidedBy, &reason, &r.Version); err != nil {
		return RewardRedemption{}, err
	}
	t, err := parseDBTime(requested)
	if err != nil {
		return RewardRedemption{}, err
	}
	r.RequestedAt = t
	if decided.Valid {
		t, err := parseDBTime(decided.String)
		if err != nil {
			return RewardRedemption{}, err
		}
		r.DecidedAt = &t
	}
	if decidedBy.Valid {
		r.DecidedByMemberID = &decidedBy.String
	}
	if reason.Valid {
		r.DecisionReason = &reason.String
	}
	return r, nil
}

// parseDBTime reads a timestamp in the format the schema stores.
func parseDBTime(s string) (time.Time, error) {
	return time.Parse(dbTimeLayout+"Z07:00", s)
}
