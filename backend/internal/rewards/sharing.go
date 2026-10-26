package rewards

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
)

// shareRef is the ledger reference type for a share, its approval and its refund.
const shareRef = "point_share"

// shareCompleted is a share whose points have reached the recipient. Pending and declined reuse
// statusPending and shareDeclined. The values match the CHECK in migration 000015.
const (
	shareCompleted = "completed"
	shareDeclined  = "declined"
)

// Notification kinds for sharing. The body text is English.
const (
	kindShareCompleted = "points_shared"
	kindSharePending   = "points_share_pending"
	kindShareDeclined  = "points_share_declined"
)

// shareNotes are the preset notes a child can attach to a share. The labels are translated in the app.
var shareNotes = []string{"thank_you", "happy_birthday", "good_job", "just_because"}

// maxShareNoteText is the longest custom note, in characters.
const maxShareNoteText = 200

// shareCols reads the share columns in the order scanShare expects. Timestamps are cast to text,
// as for redemptions.
const shareCols = `id, from_member_id, to_member_id, amount, note_key, status, approval_required,
	CAST(requested_at AS TEXT), CAST(decided_at AS TEXT), decided_by_member_id, version, transfer_id, note_text`

// Errors returned by the sharing functions. The handlers map them to HTTP responses.
var (
	ErrSharingDisabled       = errors.New("rewards: sharing is off for this family")
	ErrShareChildrenOnly     = errors.New("rewards: only a child can share points")
	ErrShareRecipientMissing = errors.New("rewards: recipient not found")
	ErrShareRecipientInvalid = errors.New("rewards: recipient must be an active sibling child")
	ErrShareNotFound         = errors.New("rewards: share not found")
	ErrShareIdemConflict     = errors.New("rewards: idempotency key was used for a different share")
)

// ShareAmountError means the amount is outside the family's per-share range, from Min to Max.
type ShareAmountError struct {
	Min int
	Max int
}

func (e *ShareAmountError) Error() string {
	return fmt.Sprintf("rewards: amount is outside the per-share range %d to %d", e.Min, e.Max)
}

// ShareDailyLimitError means the share would pass the sender's daily total. Remaining is what
// the sender may still send today.
type ShareDailyLimitError struct {
	Remaining int
}

func (e *ShareDailyLimitError) Error() string {
	return fmt.Sprintf("rewards: daily sharing limit reached, %d left today", e.Remaining)
}

// ShareBelowMinimumError means the share would leave the sender below the family's minimum balance.
type ShareBelowMinimumError struct {
	Balance int
	Minimum int
}

func (e *ShareBelowMinimumError) Error() string {
	return fmt.Sprintf("rewards: balance %d would fall below the minimum %d", e.Balance, e.Minimum)
}

// ShareSettings is the family's sharing policy. A family with no stored row uses the defaults
// and has sharing off. Version 0 means no row is stored yet.
type ShareSettings struct {
	Enabled         bool   `json:"enabled"`
	MinPerShare     int    `json:"min_per_share"`
	MaxPerShare     int    `json:"max_per_share"`
	DailyLimit      int    `json:"daily_limit"`
	MinBalance      int    `json:"min_balance"`
	ApprovalMode    string `json:"approval_mode"`
	ApprovalAbove   int    `json:"approval_above"`
	NotifyRecipient bool   `json:"notify_recipient"`
	NotifyParents   bool   `json:"notify_parents"`
	Version         int    `json:"version"`
}

// defaultShareSettings is the policy before a parent turns sharing on.
func defaultShareSettings() ShareSettings {
	return ShareSettings{
		Enabled:         false,
		MinPerShare:     5,
		MaxPerShare:     30,
		DailyLimit:      50,
		MinBalance:      10,
		ApprovalMode:    "never",
		ApprovalAbove:   20,
		NotifyRecipient: true,
		NotifyParents:   false,
		Version:         0,
	}
}

// check returns the first field that is out of range, or nil.
func (s ShareSettings) check() *fieldError {
	switch {
	case s.MaxPerShare < 1 || s.MaxPerShare > maxPoints:
		return fail("max_per_share", "must be 1 to 100000")
	case s.MinPerShare < 1 || s.MinPerShare > s.MaxPerShare:
		return fail("min_per_share", "must be 1 up to max_per_share")
	case s.DailyLimit < 1 || s.DailyLimit > maxPoints:
		return fail("daily_limit", "must be 1 to 100000")
	case s.MinBalance < 0 || s.MinBalance > maxPoints:
		return fail("min_balance", "must be 0 to 100000")
	case s.ApprovalMode != "never" && s.ApprovalMode != "above" && s.ApprovalMode != "always":
		return fail("approval_mode", "must be never, above or always")
	case s.ApprovalAbove < 0 || s.ApprovalAbove > maxPoints:
		return fail("approval_above", "must be 0 to 100000")
	}
	return nil
}

// needsApproval reports whether a share of this amount waits for a parent.
func (s ShareSettings) needsApproval(amount int) bool {
	switch s.ApprovalMode {
	case "always":
		return true
	case "above":
		return amount > s.ApprovalAbove
	}
	return false
}

// PointShare is one share as the API returns it. TransferID links the two ledger legs and is not
// sent to clients.
type PointShare struct {
	ID                string     `json:"id"`
	FromMemberID      string     `json:"from_member_id"`
	ToMemberID        string     `json:"to_member_id"`
	Amount            int        `json:"amount"`
	NoteKey           *string    `json:"note_key"`
	NoteText          *string    `json:"note_text"`
	Status            string     `json:"status"`
	ApprovalRequired  bool       `json:"approval_required"`
	RequestedAt       time.Time  `json:"requested_at"`
	DecidedAt         *time.Time `json:"decided_at"`
	DecidedByMemberID *string    `json:"decided_by_member_id"`
	Version           int        `json:"version"`
	TransferID        string     `json:"-"`
}

// ShareResult is a share with the sender's balance after the change. Replayed is true when a
// request matched a stored share and nothing was written.
type ShareResult struct {
	PointShare
	Balance  int  `json:"balance"`
	Replayed bool `json:"replayed"`
}

// ShareListItem is a share waiting for a decision, with the names the parents see.
type ShareListItem struct {
	PointShare
	FromName string `json:"from_name"`
	ToName   string `json:"to_name"`
}

// ShareRequest is the input to SendShare.
type ShareRequest struct {
	ToMemberID string
	Amount     int
	NoteKey    string
	NoteText   string
}

// ShareRecipient is a sibling a child can share with. AvatarID is nil when the sibling has no picture.
type ShareRecipient struct {
	MemberID string  `json:"member_id"`
	Name     string  `json:"name"`
	Colour   string  `json:"colour"`
	AvatarID *string `json:"avatar_id"`
}

// ShareSummary is what the sharing screens need: the policy, the sender's balance, what the
// sender has shared today and the siblings they can share with.
type ShareSummary struct {
	ShareSettings
	Balance     int              `json:"balance"`
	SharedToday int              `json:"shared_today"`
	DailyLeft   int              `json:"daily_left"`
	Notes       []string         `json:"notes"`
	Recipients  []ShareRecipient `json:"recipients"`
}

// SendShare moves points from a child to a sibling. The sender's transfer_out is posted at once.
// If the family's approval rule applies, the share waits as pending and the recipient's
// transfer_in is posted when a parent approves. Otherwise the share completes now and both legs
// are posted together. The checks run in one transaction; the database takes its write lock at
// BEGIN, so concurrent shares cannot overspend the balance or the daily limit.
//
// A repeated key from the same sender returns the stored share. The same key with a different
// request is ErrShareIdemConflict.
func SendShare(ctx context.Context, db *sql.DB, clk clock.Clock, familyID, memberID, userID string, req ShareRequest, key string) (ShareResult, error) {
	now := clk.Now()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ShareResult{}, err
	}
	defer tx.Rollback()

	existing, found, err := shareByKey(ctx, tx, familyID, memberID, key)
	if err != nil {
		return ShareResult{}, err
	}
	if found {
		if existing.ToMemberID != req.ToMemberID || existing.Amount != req.Amount || noteOf(existing) != req.NoteKey || textOf(existing) != req.NoteText {
			return ShareResult{}, ErrShareIdemConflict
		}
		res, err := shareResultOf(ctx, tx, familyID, existing.ID)
		if err != nil {
			return ShareResult{}, err
		}
		res.Replayed = true
		return res, nil
	}

	child, err := memberIsChild(ctx, tx, memberID)
	if err != nil {
		return ShareResult{}, err
	}
	if !child {
		return ShareResult{}, ErrShareChildrenOnly
	}
	s, err := loadShareSettings(ctx, tx, familyID)
	if err != nil {
		return ShareResult{}, err
	}
	if !s.Enabled {
		return ShareResult{}, ErrSharingDisabled
	}
	if req.Amount < s.MinPerShare || req.Amount > s.MaxPerShare {
		return ShareResult{}, &ShareAmountError{Min: s.MinPerShare, Max: s.MaxPerShare}
	}
	if err := checkShareRecipient(ctx, tx, familyID, memberID, req.ToMemberID); err != nil {
		return ShareResult{}, err
	}

	loc, err := familyLocation(ctx, tx, familyID)
	if err != nil {
		return ShareResult{}, err
	}
	sentToday, err := sharedSince(ctx, tx, familyID, memberID, periodStart("day", now.In(loc)))
	if err != nil {
		return ShareResult{}, err
	}
	if sentToday+req.Amount > s.DailyLimit {
		return ShareResult{}, &ShareDailyLimitError{Remaining: leftOf(s.DailyLimit, sentToday)}
	}

	balance, err := Balance(ctx, tx, familyID, memberID)
	if err != nil {
		return ShareResult{}, err
	}
	if balance-req.Amount < s.MinBalance {
		return ShareResult{}, &ShareBelowMinimumError{Balance: balance, Minimum: s.MinBalance}
	}

	needsApproval := s.needsApproval(req.Amount)
	status := shareCompleted
	var decidedAt any = dbTime(now)
	if needsApproval {
		status = statusPending
		decidedAt = nil
	}

	id := ids.New()
	transferID := ids.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO point_shares (id, family_id, from_member_id, to_member_id, amount, note_key, note_text, status,
			approval_required, transfer_id, idempotency_key, requested_at, decided_at, version, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		id, familyID, memberID, req.ToMemberID, req.Amount, nullable(req.NoteKey), nullable(req.NoteText), status,
		boolInt(needsApproval), transferID, key, dbTime(now), decidedAt, dbTime(now),
	); err != nil {
		return ShareResult{}, err
	}
	if _, err := Post(ctx, tx, now, Entry{
		FamilyID:       familyID,
		MemberID:       memberID,
		Type:           TransferOut,
		Amount:         -req.Amount,
		ReferenceType:  shareRef,
		ReferenceID:    id,
		ActorMemberID:  memberID,
		TransferID:     transferID,
		IdempotencyKey: "share-out:" + id,
	}); err != nil {
		return ShareResult{}, fmt.Errorf("post share out: %w", err)
	}

	sender, err := displayNameOf(ctx, tx, memberID)
	if err != nil {
		return ShareResult{}, err
	}
	recipient, err := displayNameOf(ctx, tx, req.ToMemberID)
	if err != nil {
		return ShareResult{}, err
	}

	if needsApproval {
		if err := notifyParents(ctx, tx, familyID, now, kindSharePending, memberID,
			fmt.Sprintf("%s wants to send %d points to %s", sender, req.Amount, recipient)); err != nil {
			return ShareResult{}, err
		}
	} else {
		if _, err := Post(ctx, tx, now, Entry{
			FamilyID:       familyID,
			MemberID:       req.ToMemberID,
			Type:           TransferIn,
			Amount:         req.Amount,
			ReferenceType:  shareRef,
			ReferenceID:    id,
			ActorMemberID:  memberID,
			TransferID:     transferID,
			IdempotencyKey: "share-in:" + id,
		}); err != nil {
			return ShareResult{}, fmt.Errorf("post share in: %w", err)
		}
		if err := notifyCompletedShare(ctx, tx, familyID, now, s, memberID, req.ToMemberID, sender, recipient, req.Amount); err != nil {
			return ShareResult{}, err
		}
	}

	if err := audit.Record(ctx, tx, familyID, userID, "points.shared", "point_share", &id,
		map[string]any{"to_member_id": req.ToMemberID, "amount": req.Amount, "status": status, "approval_required": needsApproval},
	); err != nil {
		return ShareResult{}, err
	}

	res, err := shareResultOf(ctx, tx, familyID, id)
	if err != nil {
		return ShareResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ShareResult{}, err
	}
	return res, nil
}

// ApproveShare completes a pending share. The sender's points were held when the share was
// requested, so only the recipient's transfer_in is posted now. expected is the If-Match version,
// or -1 to skip the check.
func ApproveShare(ctx context.Context, db *sql.DB, clk clock.Clock, familyID, id, userID, actorMemberID string, expected int) (ShareResult, error) {
	return decideShare(ctx, db, clk, familyID, id, userID, actorMemberID, expected, true)
}

// DeclineShare ends a pending share and refunds the sender with a reversal entry. expected is the
// If-Match version, or -1 to skip the check.
func DeclineShare(ctx context.Context, db *sql.DB, clk clock.Clock, familyID, id, userID, actorMemberID string, expected int) (ShareResult, error) {
	return decideShare(ctx, db, clk, familyID, id, userID, actorMemberID, expected, false)
}

// decideShare runs one parent decision in a single transaction.
func decideShare(ctx context.Context, db *sql.DB, clk clock.Clock, familyID, id, userID, actorMemberID string, expected int, approve bool) (ShareResult, error) {
	now := clk.Now()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ShareResult{}, err
	}
	defer tx.Rollback()

	sh, err := loadShare(ctx, tx, familyID, id)
	if err != nil {
		return ShareResult{}, err
	}
	if expected >= 0 && expected != sh.Version {
		return ShareResult{PointShare: sh}, errStaleVersion
	}
	if sh.Status != statusPending {
		return ShareResult{}, ErrNotPending
	}

	status, action := shareDeclined, "points.share_declined"
	if approve {
		status, action = shareCompleted, "points.share_approved"
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE point_shares SET status = ?, decided_at = ?, decided_by_member_id = ?,
		     version = version + 1, updated_at = ?
		 WHERE id = ? AND family_id = ? AND version = ? AND status = 'pending'`,
		status, dbTime(now), actorMemberID, dbTime(now), id, familyID, sh.Version,
	); err != nil {
		return ShareResult{}, err
	}

	sender, err := displayNameOf(ctx, tx, sh.FromMemberID)
	if err != nil {
		return ShareResult{}, err
	}
	recipient, err := displayNameOf(ctx, tx, sh.ToMemberID)
	if err != nil {
		return ShareResult{}, err
	}

	if approve {
		if _, err := Post(ctx, tx, now, Entry{
			FamilyID:       familyID,
			MemberID:       sh.ToMemberID,
			Type:           TransferIn,
			Amount:         sh.Amount,
			ReferenceType:  shareRef,
			ReferenceID:    id,
			ActorMemberID:  actorMemberID,
			TransferID:     sh.TransferID,
			IdempotencyKey: "share-in:" + id,
		}); err != nil {
			return ShareResult{}, fmt.Errorf("post share in: %w", err)
		}
		s, err := loadShareSettings(ctx, tx, familyID)
		if err != nil {
			return ShareResult{}, err
		}
		if err := notifyCompletedShare(ctx, tx, familyID, now, s, sh.FromMemberID, sh.ToMemberID, sender, recipient, sh.Amount); err != nil {
			return ShareResult{}, err
		}
	} else {
		if _, err := Post(ctx, tx, now, Entry{
			FamilyID:       familyID,
			MemberID:       sh.FromMemberID,
			Type:           Reversal,
			Amount:         sh.Amount,
			ReferenceType:  shareRef,
			ReferenceID:    id,
			ActorMemberID:  actorMemberID,
			IdempotencyKey: "share-refund:" + id,
		}); err != nil {
			return ShareResult{}, fmt.Errorf("post share refund: %w", err)
		}
		if err := insertNotification(ctx, tx, familyID, now, sh.FromMemberID, kindShareDeclined, sh.ToMemberID,
			fmt.Sprintf("Your share of %d points to %s was declined", sh.Amount, recipient)); err != nil {
			return ShareResult{}, err
		}
	}

	if err := audit.Record(ctx, tx, familyID, userID, action, "point_share", &id, map[string]any{
		"from_member_id": sh.FromMemberID,
		"to_member_id":   sh.ToMemberID,
		"amount":         sh.Amount,
		"status":         status,
	}); err != nil {
		return ShareResult{}, err
	}

	res, err := shareResultOf(ctx, tx, familyID, id)
	if err != nil {
		return ShareResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ShareResult{}, err
	}
	return res, nil
}

// PendingShares lists the family's shares that wait for a parent, oldest first.
func PendingShares(ctx context.Context, db *sql.DB, familyID string) ([]ShareListItem, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+shareCols+`,
		    (SELECT u.display_name FROM family_members m JOIN users u ON u.id = m.user_id WHERE m.id = point_shares.from_member_id),
		    (SELECT u.display_name FROM family_members m JOIN users u ON u.id = m.user_id WHERE m.id = point_shares.to_member_id)
		 FROM point_shares WHERE family_id = ? AND status = 'pending' ORDER BY requested_at, id`,
		familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ShareListItem{}
	for rows.Next() {
		var item ShareListItem
		sh, err := scanShare(rows, &item.FromName, &item.ToName)
		if err != nil {
			return nil, err
		}
		item.PointShare = sh
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// ShareStatus returns the policy and the caller's balance and daily use.
func ShareStatus(ctx context.Context, db *sql.DB, clk clock.Clock, familyID, memberID string) (ShareSummary, error) {
	s, err := loadShareSettings(ctx, db, familyID)
	if err != nil {
		return ShareSummary{}, err
	}
	loc, err := familyLocation(ctx, db, familyID)
	if err != nil {
		return ShareSummary{}, err
	}
	sent, err := sharedSince(ctx, db, familyID, memberID, periodStart("day", clk.Now().In(loc)))
	if err != nil {
		return ShareSummary{}, err
	}
	balance, err := Balance(ctx, db, familyID, memberID)
	if err != nil {
		return ShareSummary{}, err
	}
	recipients, err := shareRecipients(ctx, db, familyID, memberID)
	if err != nil {
		return ShareSummary{}, err
	}
	return ShareSummary{
		ShareSettings: s,
		Balance:       balance,
		SharedToday:   sent,
		DailyLeft:     leftOf(s.DailyLimit, sent),
		Notes:         shareNotes,
		Recipients:    recipients,
	}, nil
}

// shareRecipients lists the active sibling children of the sender, by name.
func shareRecipients(ctx context.Context, q catalogueDB, familyID, memberID string) ([]ShareRecipient, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT m.id, u.display_name, m.colour,
		        (SELECT ma.id FROM member_avatars ma WHERE ma.family_member_id = m.id LIMIT 1)
		 FROM family_members m JOIN users u ON u.id = m.user_id
		 WHERE m.family_id = ? AND m.id <> ? AND m.status = 'active' AND u.is_child = 1
		 ORDER BY u.display_name, m.id`,
		familyID, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ShareRecipient{}
	for rows.Next() {
		var r ShareRecipient
		var avatar sql.NullString
		if err := rows.Scan(&r.MemberID, &r.Name, &r.Colour, &avatar); err != nil {
			return nil, err
		}
		if avatar.Valid {
			r.AvatarID = &avatar.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadShareSettings reads the family's policy, or the defaults when no row is stored.
func loadShareSettings(ctx context.Context, q catalogueDB, familyID string) (ShareSettings, error) {
	s := defaultShareSettings()
	var enabled, notifyRecipient, notifyParents int
	err := q.QueryRowContext(ctx,
		`SELECT enabled, min_per_share, max_per_share, daily_limit, min_balance, approval_mode, approval_above,
		        notify_recipient, notify_parents, version
		 FROM points_share_settings WHERE family_id = ?`, familyID,
	).Scan(&enabled, &s.MinPerShare, &s.MaxPerShare, &s.DailyLimit, &s.MinBalance, &s.ApprovalMode, &s.ApprovalAbove,
		&notifyRecipient, &notifyParents, &s.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultShareSettings(), nil
	}
	if err != nil {
		return ShareSettings{}, err
	}
	s.Enabled = enabled == 1
	s.NotifyRecipient = notifyRecipient == 1
	s.NotifyParents = notifyParents == 1
	return s, nil
}

// SaveShareSettings replaces the family's policy. expected is the If-Match version, or -1 to skip
// the check. It returns the stored policy. On a stale version it returns the current policy with
// errStaleVersion.
func SaveShareSettings(ctx context.Context, db *sql.DB, clk clock.Clock, familyID, userID string, s ShareSettings, expected int) (ShareSettings, error) {
	now := clk.Now()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ShareSettings{}, err
	}
	defer tx.Rollback()

	current, err := loadShareSettings(ctx, tx, familyID)
	if err != nil {
		return ShareSettings{}, err
	}
	if expected >= 0 && expected != current.Version {
		return current, errStaleVersion
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO points_share_settings (family_id, enabled, min_per_share, max_per_share, daily_limit, min_balance,
			approval_mode, approval_above, notify_recipient, notify_parents, version, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (family_id) DO UPDATE SET
			enabled = excluded.enabled,
			min_per_share = excluded.min_per_share,
			max_per_share = excluded.max_per_share,
			daily_limit = excluded.daily_limit,
			min_balance = excluded.min_balance,
			approval_mode = excluded.approval_mode,
			approval_above = excluded.approval_above,
			notify_recipient = excluded.notify_recipient,
			notify_parents = excluded.notify_parents,
			version = points_share_settings.version + 1,
			updated_at = excluded.updated_at`,
		familyID, boolInt(s.Enabled), s.MinPerShare, s.MaxPerShare, s.DailyLimit, s.MinBalance,
		s.ApprovalMode, s.ApprovalAbove, boolInt(s.NotifyRecipient), boolInt(s.NotifyParents),
		current.Version+1, dbTime(now),
	); err != nil {
		return ShareSettings{}, err
	}

	if err := audit.Record(ctx, tx, familyID, userID, "points.share_settings_updated", "points_share_settings", &familyID,
		map[string]any{
			"enabled":          s.Enabled,
			"min_per_share":    s.MinPerShare,
			"max_per_share":    s.MaxPerShare,
			"daily_limit":      s.DailyLimit,
			"min_balance":      s.MinBalance,
			"approval_mode":    s.ApprovalMode,
			"approval_above":   s.ApprovalAbove,
			"notify_recipient": s.NotifyRecipient,
			"notify_parents":   s.NotifyParents,
		},
	); err != nil {
		return ShareSettings{}, err
	}

	stored, err := loadShareSettings(ctx, tx, familyID)
	if err != nil {
		return ShareSettings{}, err
	}
	if err := tx.Commit(); err != nil {
		return ShareSettings{}, err
	}
	return stored, nil
}

// checkShareRecipient confirms the recipient is an active child in the same family, other than
// the sender.
func checkShareRecipient(ctx context.Context, q catalogueDB, familyID, fromID, toID string) error {
	if toID == fromID {
		return ErrShareRecipientInvalid
	}
	var child bool
	var status string
	err := q.QueryRowContext(ctx,
		`SELECT u.is_child, m.status FROM family_members m JOIN users u ON u.id = m.user_id
		 WHERE m.id = ? AND m.family_id = ?`, toID, familyID,
	).Scan(&child, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrShareRecipientMissing
	}
	if err != nil {
		return err
	}
	if !child || status != "active" {
		return ErrShareRecipientInvalid
	}
	return nil
}

// sharedSince sums the points the member sent since the given time. Declined shares do not count.
func sharedSince(ctx context.Context, q catalogueDB, familyID, memberID string, since time.Time) (int, error) {
	var total int
	err := q.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM point_shares
		 WHERE family_id = ? AND from_member_id = ? AND status IN ('pending', 'completed') AND requested_at >= ?`,
		familyID, memberID, dbTime(since),
	).Scan(&total)
	return total, err
}

// notifyCompletedShare tells the recipient that points arrived. The parents are told too when the
// family asked for that.
func notifyCompletedShare(ctx context.Context, tx *sql.Tx, familyID string, now time.Time, s ShareSettings,
	fromID, toID, fromName, toName string, amount int) error {
	if s.NotifyRecipient {
		if err := insertNotification(ctx, tx, familyID, now, toID, kindShareCompleted, fromID,
			fmt.Sprintf("%s sent you %d points", fromName, amount)); err != nil {
			return err
		}
	}
	if s.NotifyParents {
		return notifyParents(ctx, tx, familyID, now, kindShareCompleted, fromID,
			fmt.Sprintf("%s sent %s %d points", fromName, toName, amount))
	}
	return nil
}

// notifyParents writes one notification for each active parent of the family.
func notifyParents(ctx context.Context, tx *sql.Tx, familyID string, now time.Time, kind, subjectID, body string) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT fm.id FROM family_members fm
		 JOIN roles r ON r.id = fm.role_id
		 WHERE fm.family_id = ? AND fm.status = 'active' AND r.name = 'Parent'`,
		familyID,
	)
	if err != nil {
		return err
	}
	var parentIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		parentIDs = append(parentIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, parentID := range parentIDs {
		if err := insertNotification(ctx, tx, familyID, now, parentID, kind, subjectID, body); err != nil {
			return err
		}
	}
	return nil
}

// insertNotification writes one in-app notification through the family notification policy.
func insertNotification(ctx context.Context, tx *sql.Tx, familyID string, now time.Time, recipientID, kind, subjectID, body string) error {
	_, err := notifypolicy.Notify(ctx, tx, notifypolicy.Notice{
		FamilyID: familyID, RecipientID: recipientID, Kind: kind, SubjectID: subjectID, Body: body,
	}, now)
	return err
}

// displayNameOf returns the display name of a family member.
func displayNameOf(ctx context.Context, q catalogueDB, memberID string) (string, error) {
	var name string
	err := q.QueryRowContext(ctx,
		`SELECT u.display_name FROM family_members m JOIN users u ON u.id = m.user_id WHERE m.id = ?`, memberID,
	).Scan(&name)
	return name, err
}

// shareByKey returns the sender's share stored under the idempotency key, if any.
func shareByKey(ctx context.Context, q catalogueDB, familyID, memberID, key string) (PointShare, bool, error) {
	sh, err := scanShare(q.QueryRowContext(ctx,
		`SELECT `+shareCols+` FROM point_shares WHERE family_id = ? AND from_member_id = ? AND idempotency_key = ?`,
		familyID, memberID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return PointShare{}, false, nil
	}
	if err != nil {
		return PointShare{}, false, err
	}
	return sh, true, nil
}

// loadShare reads one share of the family.
func loadShare(ctx context.Context, q catalogueDB, familyID, id string) (PointShare, error) {
	sh, err := scanShare(q.QueryRowContext(ctx,
		`SELECT `+shareCols+` FROM point_shares WHERE id = ? AND family_id = ?`, id, familyID))
	if errors.Is(err, sql.ErrNoRows) {
		return PointShare{}, ErrShareNotFound
	}
	return sh, err
}

// shareResultOf reloads a share and the sender's balance inside the caller's transaction.
func shareResultOf(ctx context.Context, tx *sql.Tx, familyID, id string) (ShareResult, error) {
	sh, err := loadShare(ctx, tx, familyID, id)
	if err != nil {
		return ShareResult{}, err
	}
	balance, err := Balance(ctx, tx, familyID, sh.FromMemberID)
	if err != nil {
		return ShareResult{}, err
	}
	return ShareResult{PointShare: sh, Balance: balance}, nil
}

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanShare reads one row in the shareCols order. Extra destinations are appended after the
// columns. It returns sql.ErrNoRows as is.
func scanShare(s rowScanner, extra ...any) (PointShare, error) {
	var (
		sh                       PointShare
		note, decided, decidedBy sql.NullString
		requested                string
		approval                 int
	)
	var noteText sql.NullString
	dest := []any{&sh.ID, &sh.FromMemberID, &sh.ToMemberID, &sh.Amount, &note, &sh.Status,
		&approval, &requested, &decided, &decidedBy, &sh.Version, &sh.TransferID, &noteText}
	dest = append(dest, extra...)
	if err := s.Scan(dest...); err != nil {
		return PointShare{}, err
	}
	t, err := parseDBTime(requested)
	if err != nil {
		return PointShare{}, err
	}
	sh.RequestedAt = t
	sh.ApprovalRequired = approval == 1
	if note.Valid {
		sh.NoteKey = &note.String
	}
	if noteText.Valid {
		sh.NoteText = &noteText.String
	}
	if decided.Valid {
		t, err := parseDBTime(decided.String)
		if err != nil {
			return PointShare{}, err
		}
		sh.DecidedAt = &t
	}
	if decidedBy.Valid {
		sh.DecidedByMemberID = &decidedBy.String
	}
	return sh, nil
}

// noteOf returns the share's note key, or "" when it has none.
func noteOf(sh PointShare) string {
	if sh.NoteKey == nil {
		return ""
	}
	return *sh.NoteKey
}

// textOf returns the share's custom note text, or "" when it has none.
func textOf(sh PointShare) string {
	if sh.NoteText == nil {
		return ""
	}
	return *sh.NoteText
}

// isShareNote reports whether s is one of the preset notes.
func isShareNote(s string) bool {
	for _, n := range shareNotes {
		if n == s {
			return true
		}
	}
	return false
}

// leftOf returns limit minus used, never below zero.
func leftOf(limit, used int) int {
	if limit-used < 0 {
		return 0
	}
	return limit - used
}
