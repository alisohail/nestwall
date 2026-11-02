package tasks

import (
	"database/sql"
	"errors"
	"net/http"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/web"
)

// MarkMissedByParent forces an open occurrence to missed, for when a child cannot mark it done.
// Only occurrences that are scheduled or past due can be forced, and at least one copy must still
// be open (to_do or rejected). Penalties are recorded the same way as for an expired occurrence.
// Requires tasks.manage.
func (h *Handlers) MarkMissedByParent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Task not found")
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin mark missed", err)
		return
	}
	defer tx.Rollback()

	current, err := loadTask(ctx, tx, familyID, id, "")
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Task not found")
		return
	}
	if err != nil {
		h.internalError(w, "load task for mark missed", err)
		return
	}
	if current.Archived {
		web.RespondError(w, http.StatusConflict, "task_archived", "This task is archived and cannot be changed", nil)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}

	occ, err := loadOccurrence(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "load occurrence for mark missed", err)
		return
	}

	var open int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_occurrence_assignees
		 WHERE family_id = ? AND task_occurrence_id = ? AND status IN ('to_do', 'rejected')`,
		familyID, occ.id,
	).Scan(&open); err != nil {
		h.internalError(w, "count open copies for mark missed", err)
		return
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE task_occurrences
		 SET status = 'missed', version = version + 1, updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE family_id = ? AND id = ? AND status IN ('scheduled', 'past_due')`,
		familyID, occ.id,
	)
	if err != nil {
		h.internalError(w, "set occurrence missed", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 || open == 0 {
		web.RespondError(w, http.StatusConflict, "task_not_open",
			"Only an open task can be marked as missed", nil)
		return
	}

	bumped, err := bumpTaskVersion(ctx, tx, familyID, current.definitionID, current.Version)
	if err != nil {
		h.internalError(w, "bump task version for mark missed", err)
		return
	}
	if !bumped {
		respondVersionConflict(w)
		return
	}

	var e expiredOccurrence
	e.id = occ.id
	e.familyID = familyID
	if err := tx.QueryRowContext(ctx,
		`SELECT d.penalty_value, d.penalty_unit, d.points
		 FROM task_occurrences o
		 JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
		 WHERE o.family_id = ? AND o.id = ?`,
		familyID, occ.id,
	).Scan(&e.penaltyValue, &e.penaltyUnit, &e.points); err != nil {
		h.internalError(w, "load penalty for mark missed", err)
		return
	}
	if e.penaltyValue.Valid && e.penaltyUnit.Valid {
		if _, err := recordPenaltyIntents(ctx, tx, e, dbTime(h.clock.Now())); err != nil {
			h.internalError(w, "record penalty for mark missed", err)
			return
		}
	}

	if err := audit.Record(ctx, tx, familyID, userID, "task.missed", "task", &id,
		map[string]any{"forced": true}); err != nil {
		h.internalError(w, "audit mark missed", err)
		return
	}

	updated, err := loadTask(ctx, tx, familyID, id, "")
	if err != nil {
		h.internalError(w, "reload task after mark missed", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit mark missed", err)
		return
	}

	setETag(w, updated.Version)
	web.RespondJSON(w, http.StatusOK, updated)
}
