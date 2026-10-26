package tasks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/web"
)

// Scopes for taking one person off a task. "occurrence" removes them from this day only.
// "series" removes them from every repeat of a repeating task.
const (
	removeScopeOccurrence = "occurrence"
	removeScopeSeries     = "series"
)

// removePersonRequest names the person to take off a task and the scope of the removal.
type removePersonRequest struct {
	MemberID string `json:"member_id"`
	Scope    string `json:"scope"`
}

// RemovePerson takes one person off a task. Everyone else keeps the task. If that person is the
// last one on the task, the whole task is archived.
//
// Scope "occurrence" deletes their copy of this day and, for a one-off task, their place on the
// task. Scope "series" also removes them from the definition, so no new repeats are made for
// them, and deletes their copies from today on, in the definition's time zone, whatever their
// status. Copies before today are kept. Points already awarded stay in the ledger.
func (h *Handlers) RemovePerson(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Task not found")
		return
	}

	var req removePersonRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !isUUID(req.MemberID) {
		respondValidation(w, &FieldError{Field: "member_id", Reason: "invalid"})
		return
	}
	if req.Scope != removeScopeOccurrence && req.Scope != removeScopeSeries {
		respondValidation(w, &FieldError{Field: "scope", Reason: "invalid"})
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin remove person", err)
		return
	}
	defer tx.Rollback()

	current, err := loadTask(ctx, tx, familyID, id, "")
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Task not found")
		return
	}
	if err != nil {
		h.internalError(w, "load task for remove person", err)
		return
	}
	if current.Archived {
		web.RespondError(w, http.StatusConflict, "task_archived", "This task is already archived", nil)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}
	if req.Scope == removeScopeSeries && !current.Repeats {
		respondValidation(w, &FieldError{Field: "scope", Reason: "not_recurring"})
		return
	}

	members, err := taskPeople(ctx, tx, familyID, current)
	if err != nil {
		h.internalError(w, "load task people", err)
		return
	}
	if !slices.Contains(members, req.MemberID) {
		notFound(w, "This person is not on the task")
		return
	}

	// The last person on the task archives it, as the Tasks list does. Nothing is deleted.
	if len(members) == 1 {
		if err := archiveDefinition(ctx, tx, familyID, userID, current); errors.Is(err, errVersionChanged) {
			web.RespondError(w, http.StatusPreconditionFailed, "version_conflict", "The task changed since you loaded it", nil)
			return
		} else if err != nil {
			h.internalError(w, "archive task after last person removed", err)
			return
		}
		h.respondReloadedTask(w, r, tx, familyID, id)
		return
	}

	if req.Scope == removeScopeOccurrence {
		if !slices.ContainsFunc(current.Assignees, func(a assignment) bool { return a.MemberID == req.MemberID }) {
			notFound(w, "This person is not on this day")
			return
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM task_occurrence_assignees WHERE family_id = ? AND task_occurrence_id = ? AND family_member_id = ?`,
			familyID, id, req.MemberID,
		); err != nil {
			h.internalError(w, "delete occurrence copy", err)
			return
		}
	} else {
		today, err := definitionToday(ctx, tx, familyID, current.definitionID, h.clock.Now())
		if err != nil {
			h.internalError(w, "work out today for series", err)
			return
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM task_occurrence_assignees WHERE family_id = ? AND id IN (
				SELECT oa.id FROM task_occurrence_assignees oa
				JOIN task_occurrences o ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id
				WHERE oa.family_id = ? AND oa.family_member_id = ? AND o.task_definition_id = ?
					AND o.due_date >= ?)`,
			familyID, familyID, req.MemberID, current.definitionID, today,
		); err != nil {
			h.internalError(w, "delete upcoming copies", err)
			return
		}
	}

	// A repeating task keeps the person's definition assignee unless the series is removed.
	if req.Scope == removeScopeSeries {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM task_definition_assignees WHERE family_id = ? AND task_definition_id = ? AND family_member_id = ?`,
			familyID, current.definitionID, req.MemberID,
		); err != nil {
			h.internalError(w, "delete definition assignee", err)
			return
		}
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE task_definitions SET version = version + 1,
			updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE id = ? AND family_id = ? AND version = ?`,
		current.definitionID, familyID, current.Version,
	)
	if err != nil {
		h.internalError(w, "bump task version", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict", "The task changed since you loaded it", nil)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "task.person_removed", "task", &id,
		map[string]any{"member_id": req.MemberID, "scope": req.Scope}); err != nil {
		h.internalError(w, "audit person removed", err)
		return
	}

	h.respondReloadedTask(w, r, tx, familyID, id)
}

// respondReloadedTask reloads the task inside the transaction, commits it and writes the task
// with its new ETag.
func (h *Handlers) respondReloadedTask(w http.ResponseWriter, r *http.Request, tx *sql.Tx, familyID, id string) {
	ctx := r.Context()
	updated, err := loadTask(ctx, tx, familyID, id, "")
	if err != nil {
		h.internalError(w, "reload task after remove person", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit remove person", err)
		return
	}
	setETag(w, updated.Version)
	web.RespondJSON(w, http.StatusOK, updated)
}

// taskPeople lists the people on a task. A repeating task's people are its definition's
// assignees. A one-off task has no definition assignees, so its people are the copies on its
// one occurrence.
func taskPeople(ctx context.Context, tx *sql.Tx, familyID string, current task) ([]string, error) {
	if current.Repeats {
		return definitionAssigneeIDs(ctx, tx, familyID, current.definitionID)
	}
	people := make([]string, 0, len(current.Assignees))
	for _, a := range current.Assignees {
		people = append(people, a.MemberID)
	}
	return people, nil
}

// definitionAssigneeIDs lists the people assigned to a definition.
func definitionAssigneeIDs(ctx context.Context, tx *sql.Tx, familyID, definitionID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT family_member_id FROM task_definition_assignees WHERE family_id = ? AND task_definition_id = ?`,
		familyID, definitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// definitionToday returns today's date in the definition's time zone.
func definitionToday(ctx context.Context, tx *sql.Tx, familyID, definitionID string, now time.Time) (string, error) {
	var zoneName string
	if err := tx.QueryRowContext(ctx,
		`SELECT time_zone FROM task_definitions WHERE family_id = ? AND id = ?`,
		familyID, definitionID,
	).Scan(&zoneName); err != nil {
		return "", err
	}
	loc, err := time.LoadLocation(zoneName)
	if err != nil {
		return "", err
	}
	return now.In(loc).Format(dateLayout), nil
}

