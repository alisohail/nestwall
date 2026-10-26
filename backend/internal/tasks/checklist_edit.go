package tasks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

// maxChecklistText is the longest checklist item, in characters.
const maxChecklistText = 200

// errChecklistItemNotFound means the item is not on this task's checklist.
var errChecklistItemNotFound = errors.New("checklist item not found")

// checklistTextRequest is the body of a checklist create or rename.
type checklistTextRequest struct {
	Text *string `json:"text"`
}

// checklistText checks and trims an item's text.
func checklistText(req checklistTextRequest) (string, error) {
	if req.Text == nil {
		return "", &FieldError{Field: "text", Reason: "required"}
	}
	text := strings.TrimSpace(*req.Text)
	if text == "" {
		return "", &FieldError{Field: "text", Reason: "required"}
	}
	if utf8.RuneCountInString(text) > maxChecklistText {
		return "", &FieldError{Field: "text", Reason: "too_long"}
	}
	return text, nil
}

// AddChecklistItem appends an item to a one-off task's checklist (POST). Requires tasks.manage.
func (h *Handlers) AddChecklistItem(w http.ResponseWriter, r *http.Request) {
	var req checklistTextRequest
	if !decodeBody(w, r, &req) {
		return
	}
	text, err := checklistText(req)
	if err != nil {
		respondValidation(w, err)
		return
	}
	h.editChecklist(w, r, http.StatusCreated, "task.checklist_added",
		func(ctx context.Context, tx *sql.Tx, familyID, occurrenceID string) (map[string]any, error) {
			return createChecklistItem(ctx, tx, familyID, occurrenceID, text)
		})
}

// RenameChecklistItem changes an item's text (PATCH). Requires tasks.manage.
func (h *Handlers) RenameChecklistItem(w http.ResponseWriter, r *http.Request) {
	itemID := r.PathValue("item")
	if !isUUID(itemID) {
		notFound(w, "Checklist item not found")
		return
	}
	var req checklistTextRequest
	if !decodeBody(w, r, &req) {
		return
	}
	text, err := checklistText(req)
	if err != nil {
		respondValidation(w, err)
		return
	}
	h.editChecklist(w, r, http.StatusOK, "task.checklist_renamed",
		func(ctx context.Context, tx *sql.Tx, familyID, occurrenceID string) (map[string]any, error) {
			return renameChecklistItem(ctx, tx, familyID, occurrenceID, itemID, text)
		})
}

// RemoveChecklistItem deletes an item from a one-off task's checklist (DELETE). Requires
// tasks.manage.
func (h *Handlers) RemoveChecklistItem(w http.ResponseWriter, r *http.Request) {
	itemID := r.PathValue("item")
	if !isUUID(itemID) {
		notFound(w, "Checklist item not found")
		return
	}
	h.editChecklist(w, r, http.StatusOK, "task.checklist_removed",
		func(ctx context.Context, tx *sql.Tx, familyID, occurrenceID string) (map[string]any, error) {
			return removeChecklistItem(ctx, tx, familyID, occurrenceID, itemID)
		})
}

// editChecklist runs one checklist change on a one-off task. The checklist is shared, so it
// changes only while every assignee is to do or rejected, and the task is not archived or
// missed. It bumps the task version, writes the change and an audit row, and responds with the
// reloaded task.
func (h *Handlers) editChecklist(w http.ResponseWriter, r *http.Request, status int, action string,
	change func(ctx context.Context, tx *sql.Tx, familyID, occurrenceID string) (map[string]any, error)) {
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
		h.internalError(w, "begin checklist change", err)
		return
	}
	defer tx.Rollback()

	current, err := loadTask(ctx, tx, familyID, id, "")
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Task not found")
		return
	}
	if err != nil {
		h.internalError(w, "load task for checklist change", err)
		return
	}
	if current.Archived {
		web.RespondError(w, http.StatusConflict, "task_archived", "This task is archived and cannot be changed", nil)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}
	for _, a := range current.Assignees {
		if a.Status != taskStatusToDo && a.Status != statusRejected {
			web.RespondError(w, http.StatusConflict, "checklist_locked",
				"The checklist can only change while everyone on the task is to do or rejected",
				map[string]any{"status": a.Status})
			return
		}
	}

	occ, err := loadOccurrence(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "load occurrence for checklist change", err)
		return
	}
	if occ.missedAt(dbTime(h.clock.Now())) {
		web.RespondError(w, http.StatusConflict, "task_missed", "The time to complete this task has passed", nil)
		return
	}

	bumped, err := bumpTaskVersion(ctx, tx, familyID, current.definitionID, current.Version)
	if err != nil {
		h.internalError(w, "bump task version for checklist change", err)
		return
	}
	if !bumped {
		respondVersionConflict(w)
		return
	}

	meta, err := change(ctx, tx, familyID, occ.id)
	if errors.Is(err, errChecklistItemNotFound) {
		notFound(w, "Checklist item not found")
		return
	}
	if err != nil {
		h.internalError(w, "write checklist change", err)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, action, "task", &id, meta); err != nil {
		h.internalError(w, "audit checklist change", err)
		return
	}

	updated, err := loadTask(ctx, tx, familyID, id, "")
	if err != nil {
		h.internalError(w, "reload task after checklist change", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit checklist change", err)
		return
	}
	setETag(w, updated.Version)
	web.RespondJSON(w, status, updated)
}

// createChecklistItem appends an item after the last one.
func createChecklistItem(ctx context.Context, tx *sql.Tx, familyID, occurrenceID, text string) (map[string]any, error) {
	var next int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(sort_order), -1) + 1 FROM task_checklist_items
		 WHERE family_id = ? AND task_occurrence_id = ?`,
		familyID, occurrenceID,
	).Scan(&next); err != nil {
		return nil, err
	}
	itemID := ids.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO task_checklist_items (id, family_id, task_occurrence_id, sort_order, item_text)
		 VALUES (?, ?, ?, ?, ?)`,
		itemID, familyID, occurrenceID, next, text,
	); err != nil {
		return nil, err
	}
	return map[string]any{"item_id": itemID}, nil
}

// renameChecklistItem changes the text of one item on the occurrence's checklist.
func renameChecklistItem(ctx context.Context, tx *sql.Tx, familyID, occurrenceID, itemID, text string) (map[string]any, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE task_checklist_items SET item_text = ?, updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE id = ? AND family_id = ? AND task_occurrence_id = ?`,
		text, itemID, familyID, occurrenceID,
	)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errChecklistItemNotFound
	}
	return map[string]any{"item_id": itemID}, nil
}

// removeChecklistItem deletes one item from the occurrence's checklist.
func removeChecklistItem(ctx context.Context, tx *sql.Tx, familyID, occurrenceID, itemID string) (map[string]any, error) {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM task_checklist_items WHERE id = ? AND family_id = ? AND task_occurrence_id = ?`,
		itemID, familyID, occurrenceID,
	)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errChecklistItemNotFound
	}
	return map[string]any{"item_id": itemID}, nil
}
