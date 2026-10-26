package tasks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/web"
)

// DeleteTask permanently deletes an archived task. Deleting it removes the whole definition: every
// person's copy, their checklist and completion history, and any skip dates. Points already awarded
// stay in the ledger. An active task returns 409, so it has to be archived first.
func (h *Handlers) DeleteTask(w http.ResponseWriter, r *http.Request) {
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
		h.internalError(w, "begin delete task", err)
		return
	}
	defer tx.Rollback()

	current, err := loadTask(ctx, tx, familyID, id, "")
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Task not found")
		return
	}
	if err != nil {
		h.internalError(w, "load task for delete", err)
		return
	}
	if !current.Archived {
		web.RespondError(w, http.StatusConflict, "task_not_archived", "Archive the task before deleting it", nil)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}

	if err := deleteDefinition(ctx, tx, familyID, userID, current); errors.Is(err, errVersionChanged) {
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict", "The task changed since you loaded it", nil)
		return
	} else if err != nil {
		h.internalError(w, "delete task", err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit delete task", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteDefinition removes the occurrences first, because their foreign key to the definition does
// not cascade. The definition's other rows cascade. The version check guards the definition delete
// against a concurrent change. The audit entry names the definition, since its occurrences are gone.
// The caller has checked that the task is archived and that its If-Match version is current.
func deleteDefinition(ctx context.Context, tx *sql.Tx, familyID, userID string, current task) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM task_occurrences WHERE family_id = ? AND task_definition_id = ?`,
		familyID, current.definitionID,
	); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM task_definitions WHERE id = ? AND family_id = ? AND version = ?`,
		current.definitionID, familyID, current.Version,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errVersionChanged
	}
	return audit.Record(ctx, tx, familyID, userID, "task.deleted", "task", &current.definitionID, nil)
}
