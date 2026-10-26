package tasks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

// repeatCreateRequest is the body of POST /api/v1/task-definitions. The task fields match a
// one-off task. The due date is the first day of the series.
type repeatCreateRequest struct {
	createTaskRequest
	RecurrenceRule string `json:"recurrence_rule"`
}

// repeatCreated is the response to a new repeating task. FirstTask is the earliest occurrence in
// the rolling window, or null when the series starts after the window.
type repeatCreated struct {
	DefinitionID   string `json:"task_definition_id"`
	RecurrenceRule string `json:"recurrence_rule"`
	Occurrences    int    `json:"occurrences"`
	FirstTask      *task  `json:"first_task"`
}

// CreateRepeat creates a repeating task definition, with the same assignees and fields as a
// one-off task, and generates its occurrences in the rolling window.
func (h *Handlers) CreateRepeat(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}

	var req repeatCreateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := req.normalise(); err != nil {
		respondValidation(w, err)
		return
	}
	rule, err := ValidateRule(req.RecurrenceRule)
	if err != nil {
		respondValidation(w, err)
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin create repeat", err)
		return
	}
	defer tx.Rollback()

	if err := applyPrefill(ctx, tx, familyID, &req.createTaskRequest); err != nil {
		h.respondFailure(w, "prefill repeat", err)
		return
	}
	if err := req.require(); err != nil {
		respondValidation(w, err)
		return
	}
	if err := checkTaskRefs(ctx, tx, familyID, req.taskFields); err != nil {
		h.respondFailure(w, "check repeat references", err)
		return
	}

	_, zoneName, err := familyZone(ctx, tx, familyID)
	if err != nil {
		h.internalError(w, "load family time zone", err)
		return
	}

	definitionID := ids.New()
	if err := insertDefinition(ctx, tx, familyID, userID, zoneName, definitionID, req.TemplateID, req.taskFields, &rule); err != nil {
		h.internalError(w, "insert repeat definition", err)
		return
	}
	if err := insertDefinitionAssignees(ctx, tx, familyID, definitionID, req.AssigneeIDs); err != nil {
		h.internalError(w, "insert repeat assignees", err)
		return
	}
	generated, err := GenerateWindow(ctx, tx, familyID, definitionID, h.clock.Now(), DefaultWindowDays)
	if err != nil {
		h.internalError(w, "generate repeat occurrences", err)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "task.created", "task", &definitionID,
		map[string]any{"title": *req.Title, "assignees": len(req.AssigneeIDs), "repeats": true}); err != nil {
		h.internalError(w, "audit repeat created", err)
		return
	}

	resp := repeatCreated{DefinitionID: definitionID, RecurrenceRule: rule, Occurrences: generated}
	firstID, err := firstOccurrenceID(ctx, tx, familyID, definitionID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		h.internalError(w, "find first occurrence", err)
		return
	default:
		first, err := loadTask(ctx, tx, familyID, firstID, "")
		if err != nil {
			h.internalError(w, "reload first occurrence", err)
			return
		}
		resp.FirstTask = &first
	}

	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit create repeat", err)
		return
	}
	web.RespondJSON(w, http.StatusCreated, resp)
}

// firstOccurrenceID returns the earliest occurrence of a definition in the family.
func firstOccurrenceID(ctx context.Context, q querier, familyID, definitionID string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx,
		`SELECT id FROM task_occurrences WHERE family_id = ? AND task_definition_id = ?
		 ORDER BY due_at LIMIT 1`,
		familyID, definitionID,
	).Scan(&id)
	return id, err
}
