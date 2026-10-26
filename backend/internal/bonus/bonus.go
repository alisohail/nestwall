// Package bonus holds the bonus (open) tasks: a parent publishes a task for one family-local day,
// eligible children grab it, complete it, and a parent approves it (PBI M4-10).
//
// Expiry is derived, not scheduled. A bonus task ends at ends_at, which defaults to the end of its
// family-local day. A task nobody finishes simply ends: no ledger row is written and no penalty
// exists for this kind of task.
package bonus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/rewards"
	"nestwall/backend/internal/tasks"
)

// Claim modes. first_come lets one child hold a task at a time. everyone_once lets each eligible
// child hold it once.
const (
	ClaimFirstCome    = "first_come"
	ClaimEveryoneOnce = "everyone_once"
)

// Claim statuses. A released claim stays in the table for history. A rejected claim is kept for the
// record too, but it holds nothing: the task is open again for a child to grab.
const (
	StatusClaimed   = "claimed"
	StatusSubmitted = "submitted"
	StatusApproved  = "approved"
	StatusRejected  = "rejected"
	StatusReleased  = "released"
)

// Task states. They are derived from the clock on every read, except cancelled, which a parent sets.
const (
	StateScheduled = "scheduled"
	StateOpen      = "open"
	StateExpired   = "expired"
	StateCancelled = "cancelled"
)

// Scope selects the tasks of the parent list. Today is the family-local day, scheduled is the days
// after it, and past is the days before it.
type Scope string

const (
	ScopeToday     Scope = "today"
	ScopeScheduled Scope = "scheduled"
	ScopePast      Scope = "past"
)

// pastLimit caps the past list.
const pastLimit = 100

const (
	maxTitle       = 200
	maxDescription = 2000
	maxNote        = 200
	maxPoints      = tasks.MaxBonusPoints
	maxEligible    = 50
	dbTimeLayout   = "2006-01-02 15:04:05.000"
	storedLayout   = "2006-01-02 15:04:05.000Z07:00"
	dayLayout      = "2006-01-02"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var dayPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Errors that the handlers map to status codes.
var (
	ErrNotFound     = errors.New("bonus: not found")
	ErrChildrenOnly = errors.New("bonus: only a child can do this")
	ErrTaken        = errors.New("bonus: already taken")
	ErrNotOpen      = errors.New("bonus: not open yet")
	ErrExpired      = errors.New("bonus: expired")
	ErrWrongState   = errors.New("bonus: wrong state for this action")
	ErrCancelled    = errors.New("bonus: cancelled")
	ErrHasClaims    = errors.New("bonus: a child has claimed this task")
)

// FieldError reports an input field that is invalid.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return "bonus: invalid " + e.Field + ": " + e.Reason }

// StaleError reports that the version sent in If-Match no longer matches the stored one.
type StaleError struct {
	Current int
}

func (e *StaleError) Error() string { return "bonus: version changed" }

// Actor is the signed-in caller, resolved from the session.
type Actor struct {
	FamilyID string
	UserID   string
	MemberID string
}

// Task is a bonus task as the API returns it. The parent view carries every claim. The child view
// carries the child's own claim and whether another child holds a first_come task.
type Task struct {
	ID                string     `json:"id"`
	TemplateID        *string    `json:"template_id"`
	Title             string     `json:"title"`
	Description       *string    `json:"description"`
	Points            int        `json:"points"`
	ClaimMode         string     `json:"claim_mode"`
	ApprovalRequired  bool       `json:"approval_required"`
	ShowFrom          *time.Time `json:"show_from"`
	EndsAt            time.Time  `json:"ends_at"`
	Day               string     `json:"day"`
	State             string     `json:"state"`
	CancelledAt       *time.Time `json:"cancelled_at"`
	Version           int        `json:"version"`
	Editable          bool       `json:"editable"`
	EligibleMemberIDs []string   `json:"eligible_member_ids"`
	Claims            []Claim    `json:"claims,omitempty"`
	MyClaim           *Claim     `json:"my_claim,omitempty"`
	TakenByOther      bool       `json:"taken_by_other,omitempty"`
}

// Claim is a child's claim on a bonus task.
type Claim struct {
	ID                string     `json:"id"`
	BonusTaskID       string     `json:"bonus_task_id"`
	MemberID          string     `json:"family_member_id"`
	Status            string     `json:"status"`
	ClaimedAt         time.Time  `json:"claimed_at"`
	ReleasedAt        *time.Time `json:"released_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	DecidedAt         *time.Time `json:"decided_at"`
	DecidedByMemberID *string    `json:"decided_by_member_id"`
	DecisionNote      *string    `json:"decision_note"`
	Version           int        `json:"version"`
}

// ClaimResult is the outcome of a claim. Replayed is true when the child already held the task.
type ClaimResult struct {
	Claim
	Replayed bool `json:"replayed"`
}

// PublishInput is the body of POST /api/v1/bonus-tasks. A template, when given, prefills the fields
// that are absent from the body. Day is a family-local date (YYYY-MM-DD), today when absent.
type PublishInput struct {
	TemplateID        *string  `json:"template_id"`
	Day               *string  `json:"day"`
	Title             *string  `json:"title"`
	Description       *string  `json:"description"`
	Points            *int     `json:"points"`
	ClaimMode         *string  `json:"claim_mode"`
	ApprovalRequired  *bool    `json:"approval_required"`
	ShowFrom          *string  `json:"show_from"`
	EndsAt            *string  `json:"ends_at"`
	EligibleMemberIDs []string `json:"eligible_member_ids"`
}

// EditInput is the body of PATCH /api/v1/bonus-tasks/{id}. An absent field keeps its value. An empty
// description or show_from clears it.
type EditInput struct {
	Day               *string  `json:"day"`
	Title             *string  `json:"title"`
	Description       *string  `json:"description"`
	Points            *int     `json:"points"`
	ClaimMode         *string  `json:"claim_mode"`
	ApprovalRequired  *bool    `json:"approval_required"`
	ShowFrom          *string  `json:"show_from"`
	EndsAt            *string  `json:"ends_at"`
	EligibleMemberIDs []string `json:"eligible_member_ids"`
}

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// Publish creates a bonus task for one family-local day. It returns the task.
func Publish(ctx context.Context, db *sql.DB, clk clock.Clock, a Actor, in PublishInput) (Task, error) {
	now := clk.Now().UTC()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback() }()

	loc, err := familyLocation(ctx, tx, a.FamilyID)
	if err != nil {
		return Task{}, err
	}
	dayStart, dayEnd, err := chooseDay(in.Day, now, loc)
	if err != nil {
		return Task{}, err
	}
	defaults, err := tasks.LoadBonusDefaults(ctx, tx, a.FamilyID)
	if err != nil {
		return Task{}, err
	}

	d, err := resolveDraft(ctx, tx, a.FamilyID, in, defaults, loc, now, dayStart, dayEnd)
	if err != nil {
		return Task{}, err
	}

	taskID := ids.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO bonus_tasks (id, family_id, template_id, title, description, points, claim_mode,
			approval_required, show_from, ends_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, a.FamilyID, nullPtr(d.templateID), d.title, nullPtr(d.description), d.points, d.claimMode,
		boolInt(d.approval), nullTime(d.showFrom), dbTime(d.endsAt), a.UserID,
	); err != nil {
		return Task{}, err
	}
	for _, memberID := range d.eligible {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO bonus_task_members (id, family_id, bonus_task_id, family_member_id)
			 VALUES (?, ?, ?, ?)`,
			ids.New(), a.FamilyID, taskID, memberID,
		); err != nil {
			return Task{}, err
		}
	}
	if err := audit.Record(ctx, tx, a.FamilyID, a.UserID, "bonus_task.published", "bonus_task", &taskID,
		map[string]any{"title": d.title, "points": d.points, "claim_mode": d.claimMode,
			"approval_required": d.approval}); err != nil {
		return Task{}, err
	}

	task, err := parentTask(ctx, tx, a.FamilyID, taskID, now, loc)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

// List returns the bonus tasks for a scope. A parent sees the tasks of the scope with all their claims:
// today is the family-local day, scheduled the days after it, and past the days before it, latest first
// and capped at pastLimit. A child sees only today's tasks offered to them that are visible, not
// cancelled and not yet ended, with their own claim. A child's scope is ignored.
func List(ctx context.Context, db *sql.DB, clk clock.Clock, a Actor, parent bool, scope Scope) ([]Task, error) {
	now := clk.Now().UTC()
	loc, err := familyLocation(ctx, db, a.FamilyID)
	if err != nil {
		return nil, err
	}
	dayStart, dayEnd := dayBounds(now, loc)

	var rows *sql.Rows
	switch {
	case !parent:
		rows, err = db.QueryContext(ctx,
			`SELECT t.id FROM bonus_tasks t
			 WHERE t.family_id = ? AND t.cancelled_at IS NULL AND t.ends_at > ? AND t.ends_at <= ?
			   AND (t.show_from IS NULL OR t.show_from <= ?)
			   AND EXISTS (SELECT 1 FROM bonus_task_members m
			               WHERE m.bonus_task_id = t.id AND m.family_member_id = ?)
			 ORDER BY t.ends_at, t.id`,
			a.FamilyID, dbTime(now), dbTime(dayEnd), dbTime(now), a.MemberID)
	case scope == ScopeScheduled:
		rows, err = db.QueryContext(ctx,
			`SELECT id FROM bonus_tasks WHERE family_id = ? AND ends_at > ? AND cancelled_at IS NULL
			 ORDER BY ends_at, id`,
			a.FamilyID, dbTime(dayEnd))
	case scope == ScopePast:
		rows, err = db.QueryContext(ctx,
			`SELECT id FROM bonus_tasks WHERE family_id = ? AND (ends_at <= ? OR cancelled_at IS NOT NULL)
			 ORDER BY ends_at DESC, id DESC LIMIT ?`,
			a.FamilyID, dbTime(dayStart), pastLimit)
	default:
		rows, err = db.QueryContext(ctx,
			`SELECT id FROM bonus_tasks WHERE family_id = ? AND ends_at > ? AND ends_at <= ? AND cancelled_at IS NULL
			 ORDER BY ends_at, id`,
			a.FamilyID, dbTime(dayStart), dbTime(dayEnd))
	}
	if err != nil {
		return nil, err
	}
	var taskIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		taskIDs = append(taskIDs, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tasks := make([]Task, 0, len(taskIDs))
	for _, id := range taskIDs {
		if parent {
			task, err := parentTask(ctx, db, a.FamilyID, id, now, loc)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, task)
			continue
		}
		task, err := loadTask(ctx, db, a.FamilyID, id)
		if err != nil {
			return nil, err
		}
		task.setSchedule(now, loc)
		live, err := taskClaims(ctx, db, a.FamilyID, id, true)
		if err != nil {
			return nil, err
		}
		for i := range live {
			if live[i].MemberID == a.MemberID {
				mine := live[i]
				task.MyClaim = &mine
			} else if task.ClaimMode == ClaimFirstCome {
				task.TakenByOther = true
			}
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// Grab takes a bonus task for the caller's child. A child who already holds a live claim gets it
// back with Replayed set and nothing is written. For first_come, a task held by another child gives
// ErrTaken.
func Grab(ctx context.Context, db *sql.DB, clk clock.Clock, a Actor, taskID string) (ClaimResult, error) {
	now := clk.Now().UTC()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ClaimResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	task, err := offeredTask(ctx, tx, a, taskID)
	if err != nil {
		return ClaimResult{}, err
	}
	loc, err := familyLocation(ctx, tx, a.FamilyID)
	if err != nil {
		return ClaimResult{}, err
	}
	_, dayStart, _ := taskDayBounds(task.EndsAt, loc)
	if task.CancelledAt != nil {
		return ClaimResult{}, ErrCancelled
	}
	if !now.Before(task.EndsAt) {
		return ClaimResult{}, ErrExpired
	}
	if now.Before(dayStart) || (task.ShowFrom != nil && now.Before(*task.ShowFrom)) {
		return ClaimResult{}, ErrNotOpen
	}

	held, err := liveClaim(ctx, tx, a.FamilyID, taskID, a.MemberID)
	switch {
	case err == nil:
		return ClaimResult{Claim: held, Replayed: true}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return ClaimResult{}, err
	}

	if task.ClaimMode == ClaimFirstCome {
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM bonus_claims
			 WHERE family_id = ? AND bonus_task_id = ? AND status NOT IN ('released', 'rejected')`,
			a.FamilyID, taskID,
		).Scan(&n); err != nil {
			return ClaimResult{}, err
		}
		if n > 0 {
			return ClaimResult{}, ErrTaken
		}
	}

	claim := Claim{
		ID:          ids.New(),
		BonusTaskID: taskID,
		MemberID:    a.MemberID,
		Status:      StatusClaimed,
		ClaimedAt:   now,
		Version:     1,
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO bonus_claims (id, family_id, bonus_task_id, family_member_id, claim_mode, status,
			claimed_at, version)
		 VALUES (?, ?, ?, ?, ?, 'claimed', ?, 1)`,
		claim.ID, a.FamilyID, taskID, a.MemberID, task.ClaimMode, dbTime(now),
	); err != nil {
		if isUniqueViolation(err) {
			return ClaimResult{}, ErrTaken
		}
		return ClaimResult{}, err
	}
	if err := audit.Record(ctx, tx, a.FamilyID, a.UserID, "bonus_claim.claimed", "bonus_claim", &claim.ID,
		map[string]any{"bonus_task_id": taskID}); err != nil {
		return ClaimResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{Claim: claim}, nil
}

// Release gives up the caller's live claim so that another child can take the task. expected is the
// claim version from If-Match, or -1 when the header is absent.
func Release(ctx context.Context, db *sql.DB, clk clock.Clock, a Actor, taskID string, expected int) (Claim, error) {
	now := clk.Now().UTC()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Claim{}, err
	}
	defer func() { _ = tx.Rollback() }()

	task, err := offeredTask(ctx, tx, a, taskID)
	if err != nil {
		return Claim{}, err
	}
	claim, err := ownLiveClaim(ctx, tx, a, taskID)
	if err != nil {
		return Claim{}, err
	}
	if err := checkVersion(expected, claim.Version); err != nil {
		return Claim{}, err
	}
	if claim.Status != StatusClaimed {
		return Claim{}, ErrWrongState
	}
	if !now.Before(task.EndsAt) {
		return Claim{}, ErrExpired
	}

	updated, err := moveClaim(ctx, tx, a.FamilyID, now, claim, StatusClaimed,
		"status = 'released', released_at = ?", dbTime(now))
	if err != nil {
		return Claim{}, err
	}
	if err := audit.Record(ctx, tx, a.FamilyID, a.UserID, "bonus_claim.released", "bonus_claim", &claim.ID,
		map[string]any{"bonus_task_id": taskID}); err != nil {
		return Claim{}, err
	}
	if err := tx.Commit(); err != nil {
		return Claim{}, err
	}
	return updated, nil
}

// Complete marks the caller's claim as done. With approval required the claim waits as submitted.
// Without it the claim is approved at once and the points are posted.
func Complete(ctx context.Context, db *sql.DB, clk clock.Clock, a Actor, taskID string, expected int) (Claim, error) {
	now := clk.Now().UTC()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Claim{}, err
	}
	defer func() { _ = tx.Rollback() }()

	task, err := offeredTask(ctx, tx, a, taskID)
	if err != nil {
		return Claim{}, err
	}
	claim, err := ownLiveClaim(ctx, tx, a, taskID)
	if err != nil {
		return Claim{}, err
	}
	if err := checkVersion(expected, claim.Version); err != nil {
		return Claim{}, err
	}
	if claim.Status != StatusClaimed {
		return Claim{}, ErrWrongState
	}
	if !now.Before(task.EndsAt) {
		return Claim{}, ErrExpired
	}

	var updated Claim
	if task.ApprovalRequired {
		updated, err = moveClaim(ctx, tx, a.FamilyID, now, claim, StatusClaimed,
			"status = 'submitted', completed_at = ?", dbTime(now))
	} else {
		updated, err = moveClaim(ctx, tx, a.FamilyID, now, claim, StatusClaimed,
			"status = 'approved', completed_at = ?, decided_at = ?", dbTime(now), dbTime(now))
		if err == nil {
			_, err = rewards.Post(ctx, tx, now, awardEntry(a.FamilyID, updated, task.Points, ""))
		}
	}
	if err != nil {
		return Claim{}, err
	}
	if err := audit.Record(ctx, tx, a.FamilyID, a.UserID, "bonus_claim.completed", "bonus_claim", &claim.ID,
		map[string]any{"bonus_task_id": taskID, "auto_approved": !task.ApprovalRequired}); err != nil {
		return Claim{}, err
	}
	if task.ApprovalRequired {
		var name string
		if err := tx.QueryRowContext(ctx,
			`SELECT u.display_name FROM family_members m JOIN users u ON u.id = m.user_id
			  WHERE m.id = ? AND m.family_id = ?`, a.MemberID, a.FamilyID,
		).Scan(&name); err != nil {
			return Claim{}, fmt.Errorf("load member name: %w", err)
		}
		if err := notifypolicy.NotifyParents(ctx, tx, notifypolicy.Notice{
			FamilyID:  a.FamilyID,
			SubjectID: a.MemberID,
			Kind:      "bonus_submitted",
			Body:      fmt.Sprintf("%s claimed \"%s\" and it needs your approval.", name, task.Title),
		}, now); err != nil {
			return Claim{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Claim{}, err
	}
	return updated, nil
}

// Approve accepts a submitted claim and posts one bonus_task_award. A claim that is already approved
// is a replay: nothing is posted again.
func Approve(ctx context.Context, db *sql.DB, clk clock.Clock, a Actor, claimID string, expected int) (ClaimResult, error) {
	now := clk.Now().UTC()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ClaimResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	claim, err := loadClaimFor(ctx, tx, a.FamilyID, claimID)
	if err != nil {
		return ClaimResult{}, err
	}
	if claim.Status == StatusApproved {
		return ClaimResult{Claim: claim, Replayed: true}, nil
	}
	if err := checkVersion(expected, claim.Version); err != nil {
		return ClaimResult{}, err
	}
	if claim.Status != StatusSubmitted {
		return ClaimResult{}, ErrWrongState
	}
	task, err := loadTask(ctx, tx, a.FamilyID, claim.BonusTaskID)
	if err != nil {
		return ClaimResult{}, err
	}

	updated, err := moveClaim(ctx, tx, a.FamilyID, now, claim, StatusSubmitted,
		"status = 'approved', decided_at = ?, decided_by_member_id = ?", dbTime(now), a.MemberID)
	if err != nil {
		return ClaimResult{}, err
	}
	if _, err := rewards.Post(ctx, tx, now, awardEntry(a.FamilyID, updated, task.Points, a.MemberID)); err != nil {
		return ClaimResult{}, err
	}
	if err := audit.Record(ctx, tx, a.FamilyID, a.UserID, "bonus_claim.approved", "bonus_claim", &claim.ID,
		map[string]any{"bonus_task_id": task.ID, "points": task.Points}); err != nil {
		return ClaimResult{}, err
	}
	if _, err := notifypolicy.Notify(ctx, tx, notifypolicy.Notice{
		FamilyID:    a.FamilyID,
		RecipientID: claim.MemberID,
		SubjectID:   claim.MemberID,
		Kind:        "bonus_approved",
		Body:        fmt.Sprintf("Your bonus \"%s\" was approved. You earned %d points.", task.Title, task.Points),
	}, now); err != nil {
		return ClaimResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{Claim: updated}, nil
}

// Reject turns down a submitted claim. Nothing is posted. A rejected claim stays live, so the child
// cannot claim the same task again that day. A claim that is already rejected is a replay.
func Reject(ctx context.Context, db *sql.DB, clk clock.Clock, a Actor, claimID string, expected int, note string) (ClaimResult, error) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxNote {
		return ClaimResult{}, &FieldError{Field: "decision_note", Reason: "too_long"}
	}
	var noteArg any
	if note != "" {
		noteArg = note
	}

	now := clk.Now().UTC()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ClaimResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	claim, err := loadClaimFor(ctx, tx, a.FamilyID, claimID)
	if err != nil {
		return ClaimResult{}, err
	}
	if claim.Status == StatusRejected {
		return ClaimResult{Claim: claim, Replayed: true}, nil
	}
	if err := checkVersion(expected, claim.Version); err != nil {
		return ClaimResult{}, err
	}
	if claim.Status != StatusSubmitted {
		return ClaimResult{}, ErrWrongState
	}

	updated, err := moveClaim(ctx, tx, a.FamilyID, now, claim, StatusSubmitted,
		"status = 'rejected', decided_at = ?, decided_by_member_id = ?, decision_note = ?",
		dbTime(now), a.MemberID, noteArg)
	if err != nil {
		return ClaimResult{}, err
	}
	if err := audit.Record(ctx, tx, a.FamilyID, a.UserID, "bonus_claim.rejected", "bonus_claim", &claim.ID,
		map[string]any{"bonus_task_id": claim.BonusTaskID}); err != nil {
		return ClaimResult{}, err
	}
	task, err := loadTask(ctx, tx, a.FamilyID, claim.BonusTaskID)
	if err != nil {
		return ClaimResult{}, err
	}
	body := fmt.Sprintf("Your bonus \"%s\" was turned down.", task.Title)
	if note != "" {
		body += " Note: " + note
	}
	if _, err := notifypolicy.Notify(ctx, tx, notifypolicy.Notice{
		FamilyID:    a.FamilyID,
		RecipientID: claim.MemberID,
		SubjectID:   claim.MemberID,
		Kind:        "bonus_rejected",
		Body:        body,
	}, now); err != nil {
		return ClaimResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{Claim: updated}, nil
}

// Edit changes a bonus task that no child has claimed and that has not ended. expected is the task
// version from If-Match, or -1 when the header is absent. Sending day moves the task to that day. Unless
// ends_at is also sent, the task then ends at the end of the new day, and show_from is cleared unless it
// is sent too. It returns the task as stored.
func Edit(ctx context.Context, db *sql.DB, clk clock.Clock, a Actor, taskID string, in EditInput, expected int) (Task, error) {
	now := clk.Now().UTC()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback() }()

	loc, err := familyLocation(ctx, tx, a.FamilyID)
	if err != nil {
		return Task{}, err
	}
	cur, err := loadTaskFor(ctx, tx, a.FamilyID, taskID)
	if err != nil {
		return Task{}, err
	}
	if err := checkVersion(expected, cur.Version); err != nil {
		return Task{}, err
	}
	if cur.CancelledAt != nil {
		return Task{}, ErrWrongState
	}
	if !now.Before(cur.EndsAt) {
		return Task{}, ErrExpired
	}
	claims, err := claimCount(ctx, tx, a.FamilyID, taskID)
	if err != nil {
		return Task{}, err
	}
	if claims > 0 {
		return Task{}, ErrHasClaims
	}

	curDay, dayStart, dayEnd := taskDayBounds(cur.EndsAt, loc)
	dayChanged := false
	if in.Day != nil {
		dayChanged = strings.TrimSpace(*in.Day) != curDay
		dayStart, dayEnd, err = chooseDay(in.Day, now, loc)
		if err != nil {
			return Task{}, err
		}
	}

	merged := PublishInput{
		Title:             &cur.Title,
		Description:       cur.Description,
		Points:            &cur.Points,
		ClaimMode:         &cur.ClaimMode,
		ApprovalRequired:  &cur.ApprovalRequired,
		EligibleMemberIDs: cur.EligibleMemberIDs,
	}
	if in.Title != nil {
		merged.Title = in.Title
	}
	if in.Description != nil {
		merged.Description = in.Description
	}
	if in.Points != nil {
		merged.Points = in.Points
	}
	if in.ClaimMode != nil {
		merged.ClaimMode = in.ClaimMode
	}
	if in.ApprovalRequired != nil {
		merged.ApprovalRequired = in.ApprovalRequired
	}
	if in.EligibleMemberIDs != nil {
		merged.EligibleMemberIDs = in.EligibleMemberIDs
	}
	// Unless the day moved, show_from and ends_at keep their stored values. A moved day ends at its own
	// end and loses its show-up time, unless the body sends them.
	if !dayChanged {
		end := cur.EndsAt.UTC().Format(time.RFC3339)
		merged.EndsAt = &end
		if cur.ShowFrom != nil {
			from := cur.ShowFrom.UTC().Format(time.RFC3339)
			merged.ShowFrom = &from
		}
	}
	if in.ShowFrom != nil {
		merged.ShowFrom = nilIfEmpty(in.ShowFrom)
	}
	if in.EndsAt != nil {
		merged.EndsAt = in.EndsAt
	}

	d, err := resolveDraft(ctx, tx, a.FamilyID, merged, tasks.BonusDefaults{}, loc, now, dayStart, dayEnd)
	if err != nil {
		return Task{}, err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE bonus_tasks SET title = ?, description = ?, points = ?, claim_mode = ?,
			approval_required = ?, show_from = ?, ends_at = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND family_id = ? AND version = ? AND cancelled_at IS NULL`,
		d.title, nullPtr(d.description), d.points, d.claimMode, boolInt(d.approval),
		nullTime(d.showFrom), dbTime(d.endsAt), dbTime(now), taskID, a.FamilyID, cur.Version,
	)
	if err != nil {
		return Task{}, err
	}
	if err := guardRows(ctx, tx, res, a.FamilyID, taskID, cur.Version); err != nil {
		return Task{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM bonus_task_members WHERE family_id = ? AND bonus_task_id = ?`,
		a.FamilyID, taskID,
	); err != nil {
		return Task{}, err
	}
	for _, memberID := range d.eligible {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO bonus_task_members (id, family_id, bonus_task_id, family_member_id)
			 VALUES (?, ?, ?, ?)`,
			ids.New(), a.FamilyID, taskID, memberID,
		); err != nil {
			return Task{}, err
		}
	}
	newDay, _, _ := taskDayBounds(d.endsAt, loc)
	if err := audit.Record(ctx, tx, a.FamilyID, a.UserID, "bonus_task.updated", "bonus_task", &taskID,
		map[string]any{"title": d.title, "points": d.points, "day": newDay, "claim_mode": d.claimMode,
			"approval_required": d.approval}); err != nil {
		return Task{}, err
	}
	task, err := parentTask(ctx, tx, a.FamilyID, taskID, now, loc)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

// Cancel withdraws a bonus task that no child has claimed. The task stays for history and children stop
// seeing it. expected is the task version from If-Match, or -1 when the header is absent.
func Cancel(ctx context.Context, db *sql.DB, clk clock.Clock, a Actor, taskID string, expected int) (Task, error) {
	now := clk.Now().UTC()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback() }()

	loc, err := familyLocation(ctx, tx, a.FamilyID)
	if err != nil {
		return Task{}, err
	}
	cur, err := loadTaskFor(ctx, tx, a.FamilyID, taskID)
	if err != nil {
		return Task{}, err
	}
	if err := checkVersion(expected, cur.Version); err != nil {
		return Task{}, err
	}
	if cur.CancelledAt != nil {
		return Task{}, ErrWrongState
	}
	if !now.Before(cur.EndsAt) {
		return Task{}, ErrExpired
	}
	claims, err := claimCount(ctx, tx, a.FamilyID, taskID)
	if err != nil {
		return Task{}, err
	}
	if claims > 0 {
		return Task{}, ErrHasClaims
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE bonus_tasks SET cancelled_at = ?, version = version + 1, updated_at = ?
		 WHERE id = ? AND family_id = ? AND version = ? AND cancelled_at IS NULL`,
		dbTime(now), dbTime(now), taskID, a.FamilyID, cur.Version,
	)
	if err != nil {
		return Task{}, err
	}
	if err := guardRows(ctx, tx, res, a.FamilyID, taskID, cur.Version); err != nil {
		return Task{}, err
	}
	if err := audit.Record(ctx, tx, a.FamilyID, a.UserID, "bonus_task.cancelled", "bonus_task", &taskID,
		map[string]any{"title": cur.Title}); err != nil {
		return Task{}, err
	}
	task, err := parentTask(ctx, tx, a.FamilyID, taskID, now, loc)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

// Unassign frees a child's claim so that the task can be claimed again. The parent takes the claim
// back and nothing is posted to the ledger. A claimed, submitted or rejected claim can be unassigned. An
// approved claim cannot, because its points are already paid. expected is the claim version from
// If-Match, or -1 when the header is absent.
func Unassign(ctx context.Context, db *sql.DB, clk clock.Clock, a Actor, claimID string, expected int) (Claim, error) {
	now := clk.Now().UTC()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Claim{}, err
	}
	defer func() { _ = tx.Rollback() }()

	claim, err := loadClaimFor(ctx, tx, a.FamilyID, claimID)
	if err != nil {
		return Claim{}, err
	}
	if err := checkVersion(expected, claim.Version); err != nil {
		return Claim{}, err
	}
	switch claim.Status {
	case StatusClaimed, StatusSubmitted, StatusRejected:
	default:
		return Claim{}, ErrWrongState
	}

	updated, err := moveClaimFrom(ctx, tx, a.FamilyID, now, claim,
		[]string{StatusClaimed, StatusSubmitted, StatusRejected},
		"status = 'released', released_at = ?, completed_at = NULL, decided_at = NULL, "+
			"decided_by_member_id = NULL, decision_note = NULL",
		dbTime(now))
	if err != nil {
		return Claim{}, err
	}
	if err := audit.Record(ctx, tx, a.FamilyID, a.UserID, "bonus_claim.unassigned", "bonus_claim", &claim.ID,
		map[string]any{"bonus_task_id": claim.BonusTaskID, "from_status": claim.Status}); err != nil {
		return Claim{}, err
	}
	if err := tx.Commit(); err != nil {
		return Claim{}, err
	}
	return updated, nil
}

// draft holds the values of a new task before they are written.
type draft struct {
	templateID  *string
	title       string
	description *string
	points      int
	claimMode   string
	approval    bool
	showFrom    *time.Time
	endsAt      time.Time
	eligible    []string
}

// resolveDraft merges the template (if any), the bonus defaults and the body into the values of a new
// task, and checks them. The body wins over the template, and the template wins over the defaults.
// A default show-from or ends time that no longer fits the day is ignored.
func resolveDraft(ctx context.Context, q querier, familyID string, in PublishInput, defaults tasks.BonusDefaults, loc *time.Location, now, dayStart, dayEnd time.Time) (draft, error) {
	d := draft{claimMode: ClaimFirstCome, approval: true}
	if defaults.Points != nil {
		d.points = *defaults.Points
	}
	if defaults.ClaimMode != nil {
		d.claimMode = *defaults.ClaimMode
	}
	if defaults.ApprovalRequired != nil {
		d.approval = *defaults.ApprovalRequired
	}

	if in.TemplateID != nil {
		tplID := strings.ToLower(strings.TrimSpace(*in.TemplateID))
		if !uuidPattern.MatchString(tplID) {
			return draft{}, fieldErr("template_id", "invalid")
		}
		var (
			kind, name             string
			tplTitle, tplDesc      sql.NullString
			tplPoints, tplApproval sql.NullInt64
		)
		err := q.QueryRowContext(ctx,
			`SELECT kind, name, title, description, default_points, approval_required
			 FROM task_templates WHERE id = ? AND family_id = ?`,
			tplID, familyID,
		).Scan(&kind, &name, &tplTitle, &tplDesc, &tplPoints, &tplApproval)
		if errors.Is(err, sql.ErrNoRows) {
			return draft{}, fieldErr("template_id", "not_found")
		}
		if err != nil {
			return draft{}, err
		}
		if kind != "bonus" {
			return draft{}, fieldErr("template_id", "not_bonus")
		}
		d.templateID = &tplID
		d.title = name
		if tplTitle.Valid && strings.TrimSpace(tplTitle.String) != "" {
			d.title = tplTitle.String
		}
		if tplDesc.Valid && tplDesc.String != "" {
			desc := tplDesc.String
			d.description = &desc
		}
		if tplPoints.Valid {
			d.points = int(tplPoints.Int64)
		}
		if tplApproval.Valid {
			d.approval = tplApproval.Int64 != 0
		}
	}

	if in.Title != nil {
		d.title = strings.TrimSpace(*in.Title)
	}
	if d.title == "" {
		return draft{}, fieldErr("title", "required")
	}
	if utf8.RuneCountInString(d.title) > maxTitle {
		return draft{}, fieldErr("title", "too_long")
	}
	if in.Description != nil {
		desc := strings.TrimSpace(*in.Description)
		d.description = nil
		if desc != "" {
			d.description = &desc
		}
	}
	if d.description != nil && utf8.RuneCountInString(*d.description) > maxDescription {
		return draft{}, fieldErr("description", "too_long")
	}
	if in.Points != nil {
		d.points = *in.Points
	}
	if d.points < 1 || d.points > maxPoints {
		return draft{}, fieldErr("points", "out_of_range")
	}
	if in.ClaimMode != nil {
		d.claimMode = *in.ClaimMode
	}
	if d.claimMode != ClaimFirstCome && d.claimMode != ClaimEveryoneOnce {
		return draft{}, fieldErr("claim_mode", "invalid")
	}
	if in.ApprovalRequired != nil {
		d.approval = *in.ApprovalRequired
	}

	seen := make(map[string]bool, len(in.EligibleMemberIDs))
	for _, raw := range in.EligibleMemberIDs {
		memberID := strings.ToLower(strings.TrimSpace(raw))
		if !uuidPattern.MatchString(memberID) {
			return draft{}, fieldErr("eligible_member_ids", "invalid")
		}
		if seen[memberID] {
			continue
		}
		seen[memberID] = true
		d.eligible = append(d.eligible, memberID)
	}
	if len(d.eligible) == 0 {
		return draft{}, fieldErr("eligible_member_ids", "required")
	}
	if len(d.eligible) > maxEligible {
		return draft{}, fieldErr("eligible_member_ids", "too_many")
	}
	for _, memberID := range d.eligible {
		child, err := activeChild(ctx, q, familyID, memberID)
		if errors.Is(err, sql.ErrNoRows) {
			return draft{}, fieldErr("eligible_member_ids", "not_found")
		}
		if err != nil {
			return draft{}, err
		}
		if !child {
			return draft{}, fieldErr("eligible_member_ids", "not_child")
		}
	}

	if in.ShowFrom != nil {
		t, err := time.Parse(time.RFC3339, *in.ShowFrom)
		if err != nil {
			return draft{}, fieldErr("show_from", "invalid")
		}
		u := t.UTC()
		d.showFrom = &u
	}
	d.endsAt = dayEnd
	if in.EndsAt != nil {
		t, err := time.Parse(time.RFC3339, *in.EndsAt)
		if err != nil {
			return draft{}, fieldErr("ends_at", "invalid")
		}
		d.endsAt = t.UTC()
	} else if defaults.Ends != nil && *defaults.Ends != tasks.EndOfDay {
		if t, ok := clockOnDay(dayStart, *defaults.Ends, loc); ok && t.After(now) && t.Before(dayEnd) {
			d.endsAt = t
		}
	}
	if d.endsAt.After(dayEnd) {
		return draft{}, fieldErr("ends_at", "after_day")
	}
	if !d.endsAt.After(dayStart) {
		return draft{}, fieldErr("ends_at", "before_day")
	}
	if !d.endsAt.After(now) {
		return draft{}, fieldErr("ends_at", "in_past")
	}
	if in.ShowFrom == nil && defaults.ShowFrom != nil {
		if t, ok := clockOnDay(dayStart, *defaults.ShowFrom, loc); ok && t.Before(d.endsAt) {
			d.showFrom = &t
		}
	}
	if d.showFrom != nil && !d.showFrom.Before(d.endsAt) {
		return draft{}, fieldErr("show_from", "after_end")
	}
	return d, nil
}

// clockOnDay returns the local HH:MM time on the family-local day that starts at dayStart.
func clockOnDay(dayStart time.Time, hhmm string, loc *time.Location) (time.Time, bool) {
	hm, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, false
	}
	local := dayStart.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), hm.Hour(), hm.Minute(), 0, 0, loc), true
}

// offeredTask loads a task the caller may act on. A parent gets ErrChildrenOnly. A task that is not
// in this family, or not offered to the child, gives ErrNotFound.
func offeredTask(ctx context.Context, q querier, a Actor, taskID string) (Task, error) {
	if err := requireChild(ctx, q, a); err != nil {
		return Task{}, err
	}
	task, err := loadTask(ctx, q, a.FamilyID, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	if !slices.Contains(task.EligibleMemberIDs, a.MemberID) {
		return Task{}, ErrNotFound
	}
	return task, nil
}

// requireChild returns ErrChildrenOnly unless the caller is an active child. A member that is not in
// the family gives ErrNotFound.
func requireChild(ctx context.Context, q querier, a Actor) error {
	child, err := activeChild(ctx, q, a.FamilyID, a.MemberID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !child {
		return ErrChildrenOnly
	}
	return nil
}

// ownLiveClaim returns the caller's live claim on a task, or ErrNotFound when there is none.
func ownLiveClaim(ctx context.Context, q querier, a Actor, taskID string) (Claim, error) {
	c, err := liveClaim(ctx, q, a.FamilyID, taskID, a.MemberID)
	if errors.Is(err, sql.ErrNoRows) {
		return Claim{}, ErrNotFound
	}
	return c, err
}

// loadClaimFor loads a claim in the family, or ErrNotFound.
func loadClaimFor(ctx context.Context, q querier, familyID, claimID string) (Claim, error) {
	c, err := loadClaim(ctx, q, familyID, claimID)
	if errors.Is(err, sql.ErrNoRows) {
		return Claim{}, ErrNotFound
	}
	return c, err
}

// moveClaim applies one status change to a claim from one status. See moveClaimFrom.
func moveClaim(ctx context.Context, tx *sql.Tx, familyID string, now time.Time, claim Claim, from, set string, args ...any) (Claim, error) {
	return moveClaimFrom(ctx, tx, familyID, now, claim, []string{from}, set, args...)
}

// moveClaimFrom applies one status change to a claim from any of the statuses in froms. The update is
// guarded by the claim's version and its status, so a concurrent change is not overwritten. It bumps the
// version and returns the claim as stored. set holds the SQL assignments and args their placeholders.
func moveClaimFrom(ctx context.Context, tx *sql.Tx, familyID string, now time.Time, claim Claim, froms []string, set string, args ...any) (Claim, error) {
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(froms)), ", ")
	args = append(args, dbTime(now), claim.ID, familyID, claim.Version)
	for _, from := range froms {
		args = append(args, from)
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE bonus_claims SET `+set+`, version = version + 1, updated_at = ?
		 WHERE id = ? AND family_id = ? AND version = ? AND status IN (`+marks+`)`,
		args...,
	)
	if err != nil {
		return Claim{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Claim{}, err
	}
	if n == 0 {
		cur, err := loadClaim(ctx, tx, familyID, claim.ID)
		if err != nil {
			return Claim{}, err
		}
		if cur.Version != claim.Version {
			return Claim{}, &StaleError{Current: cur.Version}
		}
		return Claim{}, ErrWrongState
	}
	return loadClaim(ctx, tx, familyID, claim.ID)
}

// checkVersion returns a StaleError when a version was sent and does not match the current one.
// expected is -1 when the If-Match header is absent.
func checkVersion(expected, current int) error {
	if expected >= 0 && expected != current {
		return &StaleError{Current: current}
	}
	return nil
}

// awardEntry is the ledger row for an approved bonus claim. The key is per claim, so a replayed
// approval posts nothing.
func awardEntry(familyID string, c Claim, points int, actorMemberID string) rewards.Entry {
	return rewards.Entry{
		FamilyID:       familyID,
		MemberID:       c.MemberID,
		Type:           rewards.BonusTaskAward,
		Amount:         points,
		ReferenceType:  "bonus_claim",
		ReferenceID:    c.ID,
		ActorMemberID:  actorMemberID,
		IdempotencyKey: "bonus_claim:" + c.ID,
	}
}

const taskColumns = `id, template_id, title, description, points, claim_mode, approval_required,
	CAST(show_from AS TEXT), CAST(ends_at AS TEXT), CAST(cancelled_at AS TEXT), version`

// loadTask reads a task of the family with its eligible children. It does not set the state or the day.
func loadTask(ctx context.Context, q querier, familyID, taskID string) (Task, error) {
	var (
		t         Task
		approval  int
		showFrom  sql.NullString
		endsAt    string
		cancelled sql.NullString
	)
	err := q.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM bonus_tasks WHERE id = ? AND family_id = ?`,
		taskID, familyID,
	).Scan(&t.ID, &t.TemplateID, &t.Title, &t.Description, &t.Points, &t.ClaimMode, &approval,
		&showFrom, &endsAt, &cancelled, &t.Version)
	if err != nil {
		return Task{}, err
	}
	t.ApprovalRequired = approval == 1
	if t.ShowFrom, err = optionalTime(showFrom); err != nil {
		return Task{}, err
	}
	if t.EndsAt, err = parseStored(endsAt); err != nil {
		return Task{}, err
	}
	if t.CancelledAt, err = optionalTime(cancelled); err != nil {
		return Task{}, err
	}
	t.EligibleMemberIDs, err = eligibleMembers(ctx, q, familyID, taskID)
	if err != nil {
		return Task{}, err
	}
	return t, nil
}

func eligibleMembers(ctx context.Context, q querier, familyID, taskID string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT family_member_id FROM bonus_task_members
		 WHERE family_id = ? AND bonus_task_id = ? ORDER BY family_member_id`,
		familyID, taskID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const claimColumns = `id, bonus_task_id, family_member_id, status, CAST(claimed_at AS TEXT),
	CAST(released_at AS TEXT), CAST(completed_at AS TEXT), CAST(decided_at AS TEXT),
	decided_by_member_id, decision_note, version`

func scanClaim(s rowScanner) (Claim, error) {
	var (
		c                                       Claim
		claimed                                 string
		released, completed, decided, decidedBy sql.NullString
		note                                    sql.NullString
	)
	if err := s.Scan(&c.ID, &c.BonusTaskID, &c.MemberID, &c.Status, &claimed, &released, &completed,
		&decided, &decidedBy, &note, &c.Version); err != nil {
		return Claim{}, err
	}
	var err error
	if c.ClaimedAt, err = parseStored(claimed); err != nil {
		return Claim{}, err
	}
	if c.ReleasedAt, err = optionalTime(released); err != nil {
		return Claim{}, err
	}
	if c.CompletedAt, err = optionalTime(completed); err != nil {
		return Claim{}, err
	}
	if c.DecidedAt, err = optionalTime(decided); err != nil {
		return Claim{}, err
	}
	c.DecidedByMemberID = nullStringPtr(decidedBy)
	c.DecisionNote = nullStringPtr(note)
	return c, nil
}

func loadClaim(ctx context.Context, q querier, familyID, claimID string) (Claim, error) {
	return scanClaim(q.QueryRowContext(ctx,
		`SELECT `+claimColumns+` FROM bonus_claims WHERE id = ? AND family_id = ?`,
		claimID, familyID,
	))
}

// liveClaim returns the child's claim that holds the task (not released or rejected), or sql.ErrNoRows.
func liveClaim(ctx context.Context, q querier, familyID, taskID, memberID string) (Claim, error) {
	return scanClaim(q.QueryRowContext(ctx,
		`SELECT `+claimColumns+` FROM bonus_claims
		 WHERE family_id = ? AND bonus_task_id = ? AND family_member_id = ? AND status NOT IN ('released', 'rejected')`,
		familyID, taskID, memberID,
	))
}

// taskClaims returns the claims on a task, oldest first. liveOnly leaves out released and rejected claims.
func taskClaims(ctx context.Context, q querier, familyID, taskID string, liveOnly bool) ([]Claim, error) {
	query := `SELECT ` + claimColumns + ` FROM bonus_claims WHERE family_id = ? AND bonus_task_id = ?`
	if liveOnly {
		query += ` AND status NOT IN ('released', 'rejected')`
	}
	query += ` ORDER BY claimed_at, id`
	rows, err := q.QueryContext(ctx, query, familyID, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Claim{}
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// activeChild reports whether the member is an active child of the family.
func activeChild(ctx context.Context, q querier, familyID, memberID string) (bool, error) {
	var child bool
	err := q.QueryRowContext(ctx,
		`SELECT u.is_child FROM family_members m JOIN users u ON u.id = m.user_id
		 WHERE m.id = ? AND m.family_id = ? AND m.status = 'active'`,
		memberID, familyID,
	).Scan(&child)
	return child, err
}

// familyLocation returns the family's time zone. An unknown zone falls back to UTC.
func familyLocation(ctx context.Context, q querier, familyID string) (*time.Location, error) {
	var zone string
	if err := q.QueryRowContext(ctx, `SELECT timezone FROM families WHERE id = ?`, familyID).Scan(&zone); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.UTC, nil
	}
	return loc, nil
}

// dayBounds returns the start of the family-local day of now and the start of the next day, in UTC.
func dayBounds(now time.Time, loc *time.Location) (start, end time.Time) {
	y, m, d := now.In(loc).Date()
	start = time.Date(y, m, d, 0, 0, 0, 0, loc)
	end = time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	return start.UTC(), end.UTC()
}

// taskDayBounds returns the family-local day a task belongs to, as YYYY-MM-DD, with its UTC start and
// end. A task ends at or before the end of its day, so its day is the day of the instant just before
// ends_at.
func taskDayBounds(endsAt time.Time, loc *time.Location) (string, time.Time, time.Time) {
	last := endsAt.Add(-time.Nanosecond)
	start, end := dayBounds(last, loc)
	return last.In(loc).Format(dayLayout), start, end
}

// parseDay returns the UTC bounds of the family-local day written as YYYY-MM-DD.
func parseDay(name string, loc *time.Location) (time.Time, time.Time, error) {
	if !dayPattern.MatchString(name) {
		return time.Time{}, time.Time{}, fieldErr("day", "invalid")
	}
	d, err := time.ParseInLocation(dayLayout, name, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fieldErr("day", "invalid")
	}
	return d.UTC(), d.AddDate(0, 0, 1).UTC(), nil
}

// chooseDay returns the UTC bounds of the day a task is published for. Without a day it is today. A day
// must be written YYYY-MM-DD and must not be before today in the family's time zone.
func chooseDay(day *string, now time.Time, loc *time.Location) (time.Time, time.Time, error) {
	today := now.In(loc).Format(dayLayout)
	name := today
	if day != nil {
		name = strings.TrimSpace(*day)
	}
	start, end, err := parseDay(name, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if name < today {
		return time.Time{}, time.Time{}, fieldErr("day", "in_past")
	}
	return start, end, nil
}

// nilIfEmpty returns nil for an absent or blank string, or a pointer to the trimmed string.
func nilIfEmpty(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// loadTaskFor loads a task of the family, or ErrNotFound.
func loadTaskFor(ctx context.Context, q querier, familyID, taskID string) (Task, error) {
	t, err := loadTask(ctx, q, familyID, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return t, err
}

// claimCount counts every claim on a task, released ones included.
func claimCount(ctx context.Context, q querier, familyID, taskID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bonus_claims WHERE family_id = ? AND bonus_task_id = ?`,
		familyID, taskID,
	).Scan(&n)
	return n, err
}

// guardRows checks the result of a version-guarded task update. When no row changed, the task moved on
// since it was read: a StaleError with the stored version, or ErrWrongState when the version still matches.
func guardRows(ctx context.Context, q querier, res sql.Result, familyID, taskID string, version int) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var current int
	if err := q.QueryRowContext(ctx,
		`SELECT version FROM bonus_tasks WHERE id = ? AND family_id = ?`,
		taskID, familyID,
	).Scan(&current); err != nil {
		return err
	}
	if current != version {
		return &StaleError{Current: current}
	}
	return ErrWrongState
}

// setSchedule sets the family-local day and the state derived from the clock. A cancelled task stays
// cancelled whatever the clock says.
func (t *Task) setSchedule(now time.Time, loc *time.Location) {
	var dayStart time.Time
	t.Day, dayStart, _ = taskDayBounds(t.EndsAt, loc)
	switch {
	case t.CancelledAt != nil:
		t.State = StateCancelled
	case !now.Before(t.EndsAt):
		t.State = StateExpired
	case now.Before(dayStart), t.ShowFrom != nil && now.Before(*t.ShowFrom):
		t.State = StateScheduled
	default:
		t.State = StateOpen
	}
}

// parentTask loads a task as the parent sees it: its day and state, every claim, and whether it can
// still be edited or cancelled.
func parentTask(ctx context.Context, q querier, familyID, taskID string, now time.Time, loc *time.Location) (Task, error) {
	task, err := loadTask(ctx, q, familyID, taskID)
	if err != nil {
		return Task{}, err
	}
	task.setSchedule(now, loc)
	task.Claims, err = taskClaims(ctx, q, familyID, taskID, false)
	if err != nil {
		return Task{}, err
	}
	task.Editable = len(task.Claims) == 0 && task.CancelledAt == nil && now.Before(task.EndsAt)
	return task, nil
}

func parseStored(raw string) (time.Time, error) {
	t, err := time.Parse(storedLayout, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func optionalTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid {
		return nil, nil
	}
	t, err := parseStored(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// dbTime formats a UTC instant the way the schema stores timestamps.
func dbTime(t time.Time) string {
	return t.UTC().Format(dbTimeLayout) + "+00:00"
}

func nullPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return dbTime(*t)
}

func nullStringPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func fieldErr(field, reason string) error {
	return &FieldError{Field: field, Reason: reason}
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
