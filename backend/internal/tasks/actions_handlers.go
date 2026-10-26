package tasks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

// maxDecisionNote bounds the note a parent gives when rejecting a copy.
const maxDecisionNote = 500

// auditNames maps each action to the name written to the audit log.
var auditNames = map[action]string{
	actionComplete: "task.completed",
	actionApprove:  "task.approved",
	actionReject:   "task.rejected",
	actionSkip:     "task.skipped",
	actionReopen:   "task.reopened",
	actionWithdraw: "task.withdrawn",
}

// actionRequest is the body of the actions that act on another member's copy. Complete takes an
// optional body that names the member whose copy is completed. Without one, it completes the
// signed-in member's own copy.
type actionRequest struct {
	MemberID string  `json:"member_id"`
	Note     *string `json:"note"`
}

// actorManagesTasks reports whether the signed-in member holds tasks.manage. Parents do: they may act
// on any copy, including one due later. Children may act only on their own copies that are due.
func (h *Handlers) actorManagesTasks(ctx context.Context, actorID string) (bool, error) {
	res, err := permissions.NewPolicy(h.db).Check(ctx, actorID, permissions.PermTasksManage)
	if err != nil {
		return false, err
	}
	return res.Allowed, nil
}

// Complete marks one person's copy as done. When the task needs approval the copy is submitted for
// a parent, otherwise it is approved at once. Requires tasks.complete. Completing another member's
// copy also requires tasks.manage.
func (h *Handlers) Complete(w http.ResponseWriter, r *http.Request) {
	h.perform(w, r, actionComplete)
}

// Approve accepts a submitted copy. Requires tasks.approve.
func (h *Handlers) Approve(w http.ResponseWriter, r *http.Request) {
	h.perform(w, r, actionApprove)
}

// Reject sends a submitted copy back with a required note. Requires tasks.approve.
func (h *Handlers) Reject(w http.ResponseWriter, r *http.Request) {
	h.perform(w, r, actionReject)
}

// Skip marks a to_do copy as skipped. Requires tasks.manage.
func (h *Handlers) Skip(w http.ResponseWriter, r *http.Request) {
	h.perform(w, r, actionSkip)
}

// Reopen returns a copy to to_do. A copy that was approved, skipped or rejected is reopened as
// it is. A to_do copy whose occurrence was missed gets a new grace period. Requires tasks.manage.
func (h *Handlers) Reopen(w http.ResponseWriter, r *http.Request) {
	h.perform(w, r, actionReopen)
}

// Withdraw takes a submitted copy back to to_do before a parent decides, so the child can change
// it. No points were posted at submission, so none are reversed. It acts on the caller's own copy
// unless the body names another member, which requires tasks.manage as complete does. A missed
// occurrence cannot be withdrawn.
func (h *Handlers) Withdraw(w http.ResponseWriter, r *http.Request) {
	h.perform(w, r, actionWithdraw)
}

// perform runs one action on one person's copy of a one-off task. The steps are: check the
// request, lock the task, check the version and the transition, write the copy and the task
// version, audit, and then emit events after the commit.
func (h *Handlers) perform(w http.ResponseWriter, r *http.Request, act action) {
	id := r.PathValue("id")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Task not found")
		return
	}
	actorID, _ := permissions.GetFamilyMemberID(r.Context())

	// Complete and withdraw act on the caller's copy unless the body names another member. The other
	// actions name the member in the body.
	targetID := actorID
	var note string
	if act == actionComplete || act == actionWithdraw {
		var req actionRequest
		if !decodeOptionalBody(w, r, &req) {
			return
		}
		if req.Note != nil {
			respondValidation(w, &FieldError{Field: "note", Reason: "not_allowed"})
			return
		}
		if req.MemberID != "" {
			if !isUUID(strings.ToLower(req.MemberID)) {
				respondValidation(w, &FieldError{Field: "member_id", Reason: "invalid"})
				return
			}
			targetID = strings.ToLower(req.MemberID)
		}
	} else {
		var req actionRequest
		if !decodeBody(w, r, &req) {
			return
		}
		if !isUUID(strings.ToLower(req.MemberID)) {
			respondValidation(w, &FieldError{Field: "member_id", Reason: "invalid"})
			return
		}
		targetID = strings.ToLower(req.MemberID)
		switch {
		case act == actionReject:
			if req.Note == nil || strings.TrimSpace(*req.Note) == "" {
				respondValidation(w, &FieldError{Field: "note", Reason: "required"})
				return
			}
			note = strings.TrimSpace(*req.Note)
			if utf8.RuneCountInString(note) > maxDecisionNote {
				respondValidation(w, &FieldError{Field: "note", Reason: "too_long"})
				return
			}
		case req.Note != nil:
			respondValidation(w, &FieldError{Field: "note", Reason: "not_allowed"})
			return
		}
	}
	actorManages, err := h.actorManagesTasks(r.Context(), actorID)
	if err != nil {
		h.internalError(w, "check tasks.manage for task action", err)
		return
	}
	if (act == actionComplete || act == actionWithdraw) && targetID != actorID && !actorManages {
		web.RespondError(w, http.StatusForbidden, "forbidden", "You do not have permission for this action", nil)
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin task action", err)
		return
	}
	defer tx.Rollback()

	// Complete reads only the copy it acts on, so a child never sees another member's copy.
	scope := ""
	if act == actionComplete || act == actionWithdraw {
		scope = targetID
	}
	current, err := loadTask(ctx, tx, familyID, id, scope)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Task not found")
		return
	}
	if err != nil {
		h.internalError(w, "load task for action", err)
		return
	}
	if current.Archived {
		web.RespondError(w, http.StatusConflict, "task_archived", "This task is archived and cannot be changed", nil)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}
	target, found := findAssignment(current, targetID)
	if !found {
		respondValidation(w, &FieldError{Field: "member_id", Reason: "not_found"})
		return
	}

	occ, err := loadOccurrence(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "load occurrence for action", err)
		return
	}
	now := h.clock.Now()
	stamp := dbTime(now)
	missed := occ.missedAt(stamp)
	late := occ.dueAt <= stamp
	approval := current.ApprovalRequired != nil && *current.ApprovalRequired

	to, ok := nextStatus(target.Status, act, approval)
	// A to_do copy whose occurrence was missed can be reopened into a new grace period.
	windowReopen := !ok && act == actionReopen && target.Status == taskStatusToDo && missed
	if windowReopen {
		to, ok = taskStatusToDo, true
	}
	if !ok {
		web.RespondError(w, http.StatusConflict, "invalid_transition",
			"This action is not allowed from the current status",
			map[string]any{"status": target.Status, "action": string(act)})
		return
	}
	if missed && (act == actionComplete || act == actionSkip || act == actionWithdraw) {
		web.RespondError(w, http.StatusConflict, "task_missed", "The time to complete this task has passed", nil)
		return
	}
	if act == actionComplete && targetID == actorID && !actorManages {
		notDue, err := copyNotDue(ctx, tx, familyID, occ.dueDate, now)
		if err != nil {
			h.internalError(w, "load family time zone for complete", err)
			return
		}
		if notDue {
			web.RespondError(w, http.StatusConflict, "task_not_due", "This task is not due yet",
				map[string]any{"due_date": occ.dueDate})
			return
		}
	}

	if act == actionComplete && occ.checklistAllRequired {
		var remaining int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM task_checklist_items i
			 WHERE i.family_id = ? AND i.task_occurrence_id = ?
			   AND NOT EXISTS (
				SELECT 1 FROM task_occurrence_assignees oa
				JOIN task_checklist_completions c ON c.family_id = oa.family_id AND c.task_occurrence_assignee_id = oa.id
				WHERE oa.family_id = i.family_id AND oa.task_occurrence_id = i.task_occurrence_id
				  AND oa.family_member_id = ? AND c.task_checklist_item_id = i.id)`,
			familyID, occ.id, targetID,
		).Scan(&remaining); err != nil {
			h.internalError(w, "count checklist items", err)
			return
		}
		if remaining > 0 {
			web.RespondError(w, http.StatusConflict, "checklist_incomplete",
				"Every checklist item must be ticked before the task is completed",
				map[string]any{"remaining": remaining})
			return
		}
	}

	bumped, err := bumpTaskVersion(ctx, tx, familyID, current.definitionID, current.Version)
	if err != nil {
		h.internalError(w, "bump task version", err)
		return
	}
	if !bumped {
		respondVersionConflict(w)
		return
	}

	completedLate := late
	completedAt := stamp
	if windowReopen {
		expires, err := extendedExpiry(occ, now)
		if err != nil {
			h.internalError(w, "compute extended expiry", err)
			return
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE task_occurrences SET status = ?, expires_at = ?, version = version + 1,
				updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
			 WHERE id = ? AND family_id = ? AND version = ?`,
			occurrencePastDue, expires, occ.id, familyID, occ.version)
		if err != nil {
			h.internalError(w, "reopen occurrence window", err)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			respondVersionConflict(w)
			return
		}
	} else {
		change := copyChange{status: to}
		switch act {
		case actionComplete:
			change.completedAt = &stamp
			change.completedLate = late
			if to == statusApproved {
				change.decidedAt = &stamp
			}
		case actionApprove, actionReject:
			// Approval and rejection keep when the work was done and whether it was late.
			prior, err := readCopyTimes(ctx, tx, familyID, occ.id, targetID)
			if err != nil {
				h.internalError(w, "read copy times", err)
				return
			}
			change.completedAt = prior.completedAt
			change.completedLate = prior.late
			completedLate = prior.late
			if prior.completedAt != nil {
				completedAt = *prior.completedAt
			}
			change.decidedAt = &stamp
			change.decidedBy = &actorID
			if act == actionReject {
				change.decisionNote = &note
			}
		}
		// Skip and reopen clear the completion and decision fields, which the zero copyChange does.
		written, err := setCopy(ctx, tx, familyID, occ.id, targetID, target.Status, change)
		if err != nil {
			h.internalError(w, "write assignee copy", err)
			return
		}
		if !written {
			respondVersionConflict(w)
			return
		}
	}

	if err := audit.Record(ctx, tx, familyID, userID, auditNames[act], "task", &id,
		map[string]any{
			"member_id":     targetID,
			"from":          target.Status,
			"to":            to,
			"missed":        missed,
			"window_reopen": windowReopen,
		}); err != nil {
		h.internalError(w, "audit task action", err)
		return
	}

	var events []Event
	if act == actionApprove || (act == actionComplete && to == statusApproved) {
		e := Event{Type: EventApproved, FamilyID: familyID, TaskID: id, MemberID: targetID, Late: completedLate, CompletedAt: completedAt}
		if act == actionApprove {
			e.ActorID = actorID
		}
		events = append(events, e)
	}
	if act == actionReject {
		events = append(events, Event{Type: EventRejected, FamilyID: familyID, TaskID: id, MemberID: targetID, ActorID: actorID})
	}
	if act == actionComplete && to == statusSubmitted {
		events = append(events, Event{Type: EventSubmitted, FamilyID: familyID, TaskID: id, MemberID: targetID})
	}
	if windowReopen || (act == actionReopen && target.Status == statusApproved) {
		events = append(events, Event{
			Type: EventReopened, FamilyID: familyID, TaskID: id, MemberID: targetID,
			ActorID: actorID, From: target.Status, Missed: missed,
		})
	}

	if err := h.enlist(ctx, tx, events...); err != nil {
		h.internalError(w, "run task participants", err)
		return
	}

	updated, err := loadTask(ctx, tx, familyID, id, scope)
	if err != nil {
		h.internalError(w, "reload task after action", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit task action", err)
		return
	}
	h.emit(ctx, events...)

	setETag(w, updated.Version)
	web.RespondJSON(w, http.StatusOK, updated)
}

// TickChecklist ticks or unticks one checklist step of a one-off task for one person. The step text
// is shared by the task, but the done state is kept on each person's copy, so a tick changes only
// the copy it names. The copy is the signed-in member's own, or the copy named in member_id when
// that is someone else. Ticking another member's copy requires tasks.manage. The copy must be to_do
// or rejected, and the occurrence must not be missed. Requires tasks.complete.
func (h *Handlers) TickChecklist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	itemID := r.PathValue("item")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Task not found")
		return
	}
	if !isUUID(itemID) {
		notFound(w, "Checklist item not found")
		return
	}

	var req struct {
		Done     *bool   `json:"done"`
		MemberID *string `json:"member_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Done == nil {
		respondValidation(w, &FieldError{Field: "done", Reason: "required"})
		return
	}
	actorID, _ := permissions.GetFamilyMemberID(r.Context())
	targetID := actorID
	if req.MemberID != nil {
		targetID = strings.ToLower(*req.MemberID)
		if !isUUID(targetID) {
			respondValidation(w, &FieldError{Field: "member_id", Reason: "invalid"})
			return
		}
	}

	ctx := r.Context()
	actorManages, err := h.actorManagesTasks(ctx, actorID)
	if err != nil {
		h.internalError(w, "check tasks.manage for checklist tick", err)
		return
	}
	if targetID != actorID && !actorManages {
		web.RespondError(w, http.StatusForbidden, "forbidden", "You do not have permission for this action", nil)
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin checklist tick", err)
		return
	}
	defer tx.Rollback()

	current, err := loadTask(ctx, tx, familyID, id, targetID)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Task not found")
		return
	}
	if err != nil {
		h.internalError(w, "load task for checklist", err)
		return
	}
	if current.Archived {
		web.RespondError(w, http.StatusConflict, "task_archived", "This task is archived and cannot be changed", nil)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}
	own, found := findAssignment(current, targetID)
	if !found {
		notFound(w, "Task not found")
		return
	}
	if own.Status != taskStatusToDo && own.Status != statusRejected {
		web.RespondError(w, http.StatusConflict, "checklist_locked",
			"The checklist can only change while the task is to do or rejected",
			map[string]any{"status": own.Status})
		return
	}

	occ, err := loadOccurrence(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "load occurrence for checklist", err)
		return
	}
	now := h.clock.Now()
	stamp := dbTime(now)
	if occ.missedAt(stamp) {
		web.RespondError(w, http.StatusConflict, "task_missed", "The time to complete this task has passed", nil)
		return
	}
	if targetID == actorID && !actorManages {
		notDue, err := copyNotDue(ctx, tx, familyID, occ.dueDate, now)
		if err != nil {
			h.internalError(w, "load family time zone for checklist", err)
			return
		}
		if notDue {
			web.RespondError(w, http.StatusConflict, "task_not_due", "This task is not due yet",
				map[string]any{"due_date": occ.dueDate})
			return
		}
	}

	bumped, err := bumpTaskVersion(ctx, tx, familyID, current.definitionID, current.Version)
	if err != nil {
		h.internalError(w, "bump task version for checklist", err)
		return
	}
	if !bumped {
		respondVersionConflict(w)
		return
	}

	var items int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_checklist_items WHERE family_id = ? AND id = ? AND task_occurrence_id = ?`,
		familyID, itemID, occ.id,
	).Scan(&items); err != nil {
		h.internalError(w, "check checklist item", err)
		return
	}
	if items == 0 {
		notFound(w, "Checklist item not found")
		return
	}

	if *req.Done {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO task_checklist_completions (family_id, task_occurrence_assignee_id, task_checklist_item_id, completed_at, completed_by_member_id)
			 SELECT ?, oa.id, ?, ?, ? FROM task_occurrence_assignees oa
			 WHERE oa.family_id = ? AND oa.task_occurrence_id = ? AND oa.family_member_id = ?
			 ON CONFLICT (task_occurrence_assignee_id, task_checklist_item_id) DO NOTHING`,
			familyID, itemID, stamp, actorID, familyID, occ.id, targetID)
	} else {
		_, err = tx.ExecContext(ctx,
			`DELETE FROM task_checklist_completions
			 WHERE family_id = ? AND task_checklist_item_id = ?
			   AND task_occurrence_assignee_id IN (
				SELECT id FROM task_occurrence_assignees
				WHERE family_id = ? AND task_occurrence_id = ? AND family_member_id = ?)`,
			familyID, itemID, familyID, occ.id, targetID)
	}
	if err != nil {
		h.internalError(w, "write checklist completion", err)
		return
	}

	if err := audit.Record(ctx, tx, familyID, userID, "task.checklist_updated", "task", &id,
		map[string]any{"item_id": itemID, "done": *req.Done, "member_id": targetID}); err != nil {
		h.internalError(w, "audit checklist tick", err)
		return
	}

	updated, err := loadTask(ctx, tx, familyID, id, targetID)
	if err != nil {
		h.internalError(w, "reload task after checklist tick", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit checklist tick", err)
		return
	}

	setETag(w, updated.Version)
	web.RespondJSON(w, http.StatusOK, updated)
}

// findAssignment returns the assignee row of one member on a task.
func findAssignment(t task, memberID string) (assignment, bool) {
	for _, a := range t.Assignees {
		if a.MemberID == memberID {
			return a, true
		}
	}
	return assignment{}, false
}

// respondVersionConflict writes the 412 response for a write that lost a race.
func respondVersionConflict(w http.ResponseWriter) {
	web.RespondError(w, http.StatusPreconditionFailed, "version_conflict", "The task changed since you loaded it", nil)
}
