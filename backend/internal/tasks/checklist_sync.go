package tasks

import (
	"context"
	"database/sql"

	"nestwall/backend/internal/platform/ids"
)

// Owner columns of task_checklist_items. A one-off or occurrence owns its rows by occurrence, and a
// repeating definition owns the rows that new occurrences copy.
const (
	ownerOccurrence = "task_occurrence_id"
	ownerDefinition = "task_definition_id"
)

// checklistEditItem is one step of a checklist sent with a task edit. ID is set for a step that
// already exists, so its completions survive a change of text.
type checklistEditItem struct {
	ID   *string `json:"id"`
	Text string  `json:"text"`
}

// validateChecklistEdit checks each step and returns them with trimmed text, in order.
func validateChecklistEdit(items []checklistEditItem) ([]checklistEditItem, error) {
	out := make([]checklistEditItem, 0, len(items))
	for _, item := range items {
		text, err := checklistText(checklistTextRequest{Text: &item.Text})
		if err != nil {
			return nil, err
		}
		out = append(out, checklistEditItem{ID: item.ID, Text: text})
	}
	return out, nil
}

// checklistRowIDs lists the rows an owner has, in display order.
func checklistRowIDs(ctx context.Context, tx *sql.Tx, familyID, column, ownerID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM task_checklist_items WHERE family_id = ? AND `+column+` = ? ORDER BY sort_order`,
		familyID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// replaceChecklistByID makes an owner's checklist match items. A step whose ID is still on the
// owner keeps its row, so its completions stay. Other steps are new rows, and rows that are no
// longer listed are deleted.
func replaceChecklistByID(ctx context.Context, tx *sql.Tx, familyID, column, ownerID string, items []checklistEditItem) error {
	existing, err := checklistRowIDs(ctx, tx, familyID, column, ownerID)
	if err != nil {
		return err
	}
	available := make(map[string]bool, len(existing))
	for _, id := range existing {
		available[id] = true
	}
	claimed := make(map[string]bool, len(items))
	keep := make([]string, len(items))
	for i, item := range items {
		if item.ID != nil && available[*item.ID] {
			available[*item.ID] = false
			claimed[*item.ID] = true
			keep[i] = *item.ID
		}
	}
	for _, id := range existing {
		if claimed[id] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM task_checklist_items WHERE family_id = ? AND id = ?`, familyID, id); err != nil {
			return err
		}
	}
	// Move kept rows out of the way so that their sort orders can be reassigned without clashing.
	if _, err := tx.ExecContext(ctx,
		`UPDATE task_checklist_items SET sort_order = sort_order + 100000
		 WHERE family_id = ? AND `+column+` = ?`, familyID, ownerID); err != nil {
		return err
	}
	for i, item := range items {
		if keep[i] != "" {
			if _, err := tx.ExecContext(ctx,
				`UPDATE task_checklist_items SET item_text = ?, sort_order = ?,
					updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
				 WHERE family_id = ? AND id = ?`, item.Text, i, familyID, keep[i]); err != nil {
				return err
			}
			continue
		}
		if err := insertChecklistRow(ctx, tx, familyID, column, ownerID, i, item.Text); err != nil {
			return err
		}
	}
	return nil
}

// replaceChecklistByPosition makes an owner's checklist match texts, row by row in display order.
// It keeps the row at each position and is used where the steps have no ID for that owner, such
// as a repeating definition and its future occurrences.
func replaceChecklistByPosition(ctx context.Context, tx *sql.Tx, familyID, column, ownerID string, items []checklistEditItem) error {
	existing, err := checklistRowIDs(ctx, tx, familyID, column, ownerID)
	if err != nil {
		return err
	}
	for _, id := range existing[min(len(existing), len(items)):] {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM task_checklist_items WHERE family_id = ? AND id = ?`, familyID, id); err != nil {
			return err
		}
	}
	for i, item := range items {
		if i < len(existing) {
			if _, err := tx.ExecContext(ctx,
				`UPDATE task_checklist_items SET item_text = ?, sort_order = ?,
					updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
				 WHERE family_id = ? AND id = ?`, item.Text, i, familyID, existing[i]); err != nil {
				return err
			}
			continue
		}
		if err := insertChecklistRow(ctx, tx, familyID, column, ownerID, i, item.Text); err != nil {
			return err
		}
	}
	return nil
}

func insertChecklistRow(ctx context.Context, tx *sql.Tx, familyID, column, ownerID string, order int, text string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO task_checklist_items (id, family_id, `+column+`, sort_order, item_text)
		 VALUES (?, ?, ?, ?, ?)`,
		ids.New(), familyID, ownerID, order, text)
	return err
}

// openFutureOccurrences lists the other occurrences of a series that are still due and where
// nobody has acted yet. Their checklists follow a series edit.
func openFutureOccurrences(ctx context.Context, tx *sql.Tx, familyID, definitionID, exceptID, nowText string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT o.id FROM task_occurrences o
		 WHERE o.family_id = ? AND o.task_definition_id = ? AND o.id <> ? AND o.due_at >= ?
		   AND NOT EXISTS (
			SELECT 1 FROM task_occurrence_assignees a
			WHERE a.family_id = o.family_id AND a.task_occurrence_id = o.id
			  AND a.status NOT IN (?, ?))`,
		familyID, definitionID, exceptID, nowText, taskStatusToDo, statusRejected)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
