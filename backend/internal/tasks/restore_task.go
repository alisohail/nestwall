package tasks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/web"
)

// UnarchiveTask restores an archived task to its state before it was archived. Only a task that was
// archived as a whole can be restored: by archiving it, or by removing its last person. A task that a
// recurrence edit split or replaced is not restored, because its copies now belong to other
// definitions. An active task returns 409.
func (h *Handlers) UnarchiveTask(w http.ResponseWriter, r *http.Request) {
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
		h.internalError(w, "begin restore task", err)
		return
	}
	defer tx.Rollback()

	current, err := loadTask(ctx, tx, familyID, id, "")
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Task not found")
		return
	}
	if err != nil {
		h.internalError(w, "load task for restore", err)
		return
	}
	if !current.Archived {
		web.RespondError(w, http.StatusConflict, "task_not_archived", "This task is not archived", nil)
		return
	}
	wholeArchive, err := isWholeArchive(ctx, tx, familyID, current.ID)
	if err != nil {
		h.internalError(w, "check archive history", err)
		return
	}
	if !wholeArchive {
		web.RespondError(w, http.StatusConflict, "task_not_restorable", "This task cannot be restored", nil)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}

	if err := restoreDefinition(ctx, tx, familyID, userID, current); errors.Is(err, errVersionChanged) {
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict", "The task changed since you loaded it", nil)
		return
	} else if err != nil {
		h.internalError(w, "restore task", err)
		return
	}

	restored, err := loadTask(ctx, tx, familyID, id, "")
	if err != nil {
		h.internalError(w, "reload restored task", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit restore task", err)
		return
	}
	setETag(w, restored.Version)
	web.RespondJSON(w, http.StatusOK, restored)
}

// isWholeArchive reports whether the task's most recent archive-related audit entry is task.archived.
// Recurrence edits write their own entries, or none, so a task they archived is not whole.
func isWholeArchive(ctx context.Context, q querier, familyID, taskID string) (bool, error) {
	var action string
	err := q.QueryRowContext(ctx,
		`SELECT action FROM audit_logs
		 WHERE family_id = ? AND target_id = ?
		   AND action IN ('task.archived', 'task.restored', 'task.series_split',
		                  'task.occurrence_detached', 'task.series_rule_changed')
		 ORDER BY rowid DESC LIMIT 1`,
		familyID, taskID,
	).Scan(&action)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return action == "task.archived", nil
}

// restoreDefinition makes the task's definition active again, which shows every copy to its people
// once more, and records the audit entry. The caller has checked that the task is archived and that
// its If-Match version is current.
func restoreDefinition(ctx context.Context, tx *sql.Tx, familyID, userID string, current task) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE task_definitions SET status = 'active', version = version + 1,
			updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE id = ? AND family_id = ? AND version = ?`,
		current.definitionID, familyID, current.Version,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errVersionChanged
	}
	return audit.Record(ctx, tx, familyID, userID, "task.restored", "task", &current.ID, nil)
}
