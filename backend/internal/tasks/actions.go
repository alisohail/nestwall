package tasks

import (
	"context"
	"database/sql"
	"time"
)

// M3-07 moves one person's copy of a one-off task through complete, approve, reject, skip and
// reopen, and ticks checklist items. Withdraw takes a submitted copy back to to_do before a parent decides. Each action runs in one transaction. It advances the
// definition version with a guarded update, so a stale write returns 412, and it is audited.
// Points are not posted here. M4 registers a Participant that posts them in the action's transaction.

// action is a change to one person's copy of a task.
type action string

const (
	actionComplete action = "complete"
	actionApprove  action = "approve"
	actionReject   action = "reject"
	actionSkip     action = "skip"
	actionReopen   action = "reopen"
	actionWithdraw action = "withdraw"
)

// Assignee statuses added in M3-07. The to_do status is taskStatusToDo.
const (
	statusSubmitted = "submitted"
	statusApproved  = "approved"
	statusRejected  = "rejected"
	statusSkipped   = "skipped"
)

// occurrenceMissed is the occurrence status set once the task has expired unfinished.
const occurrenceMissed = "missed"

// occurrencePastDue is the occurrence status while its grace period is open.
const occurrencePastDue = "past_due"

// storedTimeLayout parses the stored due and expiry text, which carries an offset.
const storedTimeLayout = dbTimeLayout + "Z07:00"

// Event types raised after an action commits.
const (
	EventApproved  = "task.approved"
	EventReopened  = "task.reopened"
	EventRejected  = "task.rejected"
	EventSubmitted = "task.submitted"
)

// Event describes a committed change that other packages act on. M4 posts points on
// EventApproved and reverses them on EventReopened.
type Event struct {
	Type     string // EventApproved or EventReopened
	FamilyID string
	TaskID   string // the occurrence ID
	MemberID string // the person whose copy changed
	ActorID  string // the member who acted; empty when the approval was automatic
	Late     bool   // the copy was completed after its due time
	From     string // the copy's status before a reopen
	Missed   bool   // the occurrence was missed when the copy was reopened
	// CompletedAt is when the work was done (the stored completed_at text). It is set on
	// EventApproved so a listener can tell one completion from a later one after a reopen.
	CompletedAt string
}

// nextStatus returns the status a copy moves to for an action, and whether the move is allowed.
// approvalRequired decides whether a completion waits for a parent.
func nextStatus(from string, act action, approvalRequired bool) (string, bool) {
	switch act {
	case actionComplete:
		if from != taskStatusToDo && from != statusRejected {
			return "", false
		}
		if approvalRequired {
			return statusSubmitted, true
		}
		return statusApproved, true
	case actionApprove:
		if from == statusSubmitted {
			return statusApproved, true
		}
	case actionReject:
		if from == statusSubmitted {
			return statusRejected, true
		}
	case actionSkip:
		if from == taskStatusToDo {
			return statusSkipped, true
		}
	case actionReopen:
		switch from {
		case statusApproved, statusSkipped, statusRejected:
			return taskStatusToDo, true
		}
	case actionWithdraw:
		if from == statusSubmitted {
			return taskStatusToDo, true
		}
	}
	return "", false
}

// occurrenceState is the part of a task's occurrence that the actions read.
type occurrenceState struct {
	id                   string
	status               string
	version              int
	dueDate              string
	dueAt                string
	expiresAt            *string
	checklistAllRequired bool
}

// missedAt reports whether the occurrence is missed at the given stored timestamp. An occurrence
// is missed once its status says so, or once its expiry has passed.
func (o occurrenceState) missedAt(stamp string) bool {
	if o.status == occurrenceMissed {
		return true
	}
	return o.expiresAt != nil && *o.expiresAt <= stamp
}

// loadOccurrence reads a task occurrence by its ID, which is the task ID.
func loadOccurrence(ctx context.Context, q querier, familyID, occurrenceID string) (occurrenceState, error) {
	var o occurrenceState
	var required int
	err := q.QueryRowContext(ctx,
		`SELECT o.id, o.status, o.version, CAST(o.due_date AS TEXT), CAST(o.due_at AS TEXT),
			CAST(o.expires_at AS TEXT), d.checklist_all_required
		 FROM task_occurrences o
		 JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
		 WHERE o.family_id = ? AND o.id = ?`,
		familyID, occurrenceID,
	).Scan(&o.id, &o.status, &o.version, &o.dueDate, &o.dueAt, &o.expiresAt, &required)
	if err != nil {
		return occurrenceState{}, err
	}
	o.checklistAllRequired = required == 1
	return o, nil
}

// extendedExpiry returns the expiry for a reopened missed occurrence. It keeps the original grace
// period and counts it from now. A task that never expires keeps no expiry.
func extendedExpiry(o occurrenceState, now time.Time) (*string, error) {
	if o.expiresAt == nil {
		return nil, nil
	}
	due, err := time.Parse(storedTimeLayout, o.dueAt)
	if err != nil {
		return nil, err
	}
	expires, err := time.Parse(storedTimeLayout, *o.expiresAt)
	if err != nil {
		return nil, err
	}
	next := dbTime(now.Add(expires.Sub(due)))
	return &next, nil
}

// bumpTaskVersion advances the definition version when the caller's version still matches. It
// reports false when the task changed since the caller read it. Pass the definition ID.
func bumpTaskVersion(ctx context.Context, tx *sql.Tx, familyID, definitionID string, version int) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE task_definitions SET version = version + 1,
			updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE id = ? AND family_id = ? AND version = ?`,
		definitionID, familyID, version)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// copyChange is the new state of one person's copy. Nil timestamps and names are stored as NULL.
type copyChange struct {
	status        string
	completedAt   *string
	completedLate bool
	decidedAt     *string
	decidedBy     *string
	decisionNote  *string
}

// setCopy writes a person's copy. The update applies only while the copy still has the status
// it had when it was read. It reports false when the copy moved on in the meantime.
func setCopy(ctx context.Context, tx *sql.Tx, familyID, occurrenceID, memberID, from string, c copyChange) (bool, error) {
	late := 0
	if c.completedLate {
		late = 1
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE task_occurrence_assignees SET status = ?, completed_at = ?, completed_late = ?,
			decided_at = ?, decided_by_member_id = ?, decision_note = ?, version = version + 1,
			updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE family_id = ? AND task_occurrence_id = ? AND family_member_id = ? AND status = ?`,
		c.status, c.completedAt, late, c.decidedAt, c.decidedBy, c.decisionNote,
		familyID, occurrenceID, memberID, from)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// copyTimes is when a person's copy was completed and whether that was late.
type copyTimes struct {
	completedAt *string
	late        bool
}

// readCopyTimes returns the completion time and lateness of a person's copy. Approval and
// rejection keep these, so the record still shows when the work was done.
func readCopyTimes(ctx context.Context, q querier, familyID, occurrenceID, memberID string) (copyTimes, error) {
	var t copyTimes
	var late int
	err := q.QueryRowContext(ctx,
		`SELECT CAST(completed_at AS TEXT), completed_late FROM task_occurrence_assignees
		 WHERE family_id = ? AND task_occurrence_id = ? AND family_member_id = ?`,
		familyID, occurrenceID, memberID,
	).Scan(&t.completedAt, &late)
	t.late = late == 1
	return t, err
}

// Participant writes inside an action's transaction. It runs for each event before the transaction
// commits, so its writes succeed or fail together with the action. An error rolls the action back.
type Participant func(ctx context.Context, tx *sql.Tx, e Event) error

// Participate registers a Participant. Call it at startup, before the server takes requests.
func (h *Handlers) Participate(fn Participant) {
	h.participants = append(h.participants, fn)
}

// enlist runs every Participant for each event, inside the action's transaction.
func (h *Handlers) enlist(ctx context.Context, tx *sql.Tx, events ...Event) error {
	for _, e := range events {
		for _, fn := range h.participants {
			if err := fn(ctx, tx, e); err != nil {
				return err
			}
		}
	}
	return nil
}

// Subscribe registers a listener for the events raised after an action commits. Call it at
// startup, before the server takes requests. Listeners run in the request goroutine.
func (h *Handlers) Subscribe(fn func(ctx context.Context, e Event)) {
	h.listeners = append(h.listeners, fn)
}

// emit calls every listener with each event.
func (h *Handlers) emit(ctx context.Context, events ...Event) {
	for _, e := range events {
		for _, fn := range h.listeners {
			fn(ctx, e)
		}
	}
}
