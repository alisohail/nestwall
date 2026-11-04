package tasks

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/web"
)

// MarkMissedByParent skips open copies of an occurrence, for when a child cannot mark it done.
// A member_id in the body skips only that person's copy. Without one, every open copy is skipped.
// Skipped copies are not penalised and leave the task lists. Requires tasks.manage.
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
	var req actionRequest
	if !decodeOptionalBody(w, r, &req) {
		return
	}
	memberID := ""
	if req.MemberID != "" {
		if !isUUID(strings.ToLower(req.MemberID)) {
			respondValidation(w, &FieldError{Field: "member_id", Reason: "invalid"})
			return
		}
		memberID = strings.ToLower(req.MemberID)
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

	query := `UPDATE task_occurrence_assignees
		 SET status = 'skipped', completed_at = NULL, decided_at = NULL, decided_by_member_id = NULL,
		     decision_note = NULL, version = version + 1, updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE family_id = ? AND task_occurrence_id = ? AND status IN ('to_do', 'rejected')`
	args := []any{familyID, occ.id}
	if memberID != "" {
		query += ` AND family_member_id = ?`
		args = append(args, memberID)
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		h.internalError(w, "skip open copies", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		web.RespondError(w, http.StatusConflict, "task_not_open",
			"Only an open task can be marked as skipped", nil)
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

	auditData := map[string]any{"forced": true, "by_parent": true}
	if memberID != "" {
		auditData["member_id"] = memberID
	}
	if err := audit.Record(ctx, tx, familyID, userID, "task.skipped", "task", &id, auditData); err != nil {
		h.internalError(w, "audit mark skipped", err)
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
