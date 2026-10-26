package tasks

import (
	"net/http"
	"testing"
)

// deleteTaskPath is the permanent-delete route for a task.
func deleteTaskPath(taskID string) string { return tasksPath + "/" + taskID }

// countRows counts the rows a query returns, with one argument per placeholder.
func countRows(t *testing.T, f fixture, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// addTaskAward writes a task_award ledger row that points at a task, as an approval would.
func addTaskAward(t *testing.T, f fixture, memberID, taskID string) {
	t.Helper()
	if _, err := f.db.Exec(
		`INSERT INTO points_ledger (id, family_id, family_member_id, entry_type, amount,
			reference_type, reference_id, idempotency_key)
		 VALUES ('ledger-award-1', ?, ?, 'task_award', 5, 'task_award', ?, 'award-key-1')`,
		f.a.FamilyID, memberID, taskID,
	); err != nil {
		t.Fatalf("add task award: %v", err)
	}
}

func TestDeleteArchivedOneOffRemovesTaskAndKeepsPoints(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID, child.memberID))
	defID := definitionOf(t, f, created.ID)
	addTaskAward(t, f, f.parent.memberID, created.ID)

	expectStatus(t, f.do(t, f.parent, http.MethodPost, tasksPath+"/"+created.ID+"/archive", nil, map[string]string{"If-Match": `"1"`}), http.StatusOK)

	rec := f.do(t, f.parent, http.MethodDelete, deleteTaskPath(created.ID), nil, ifMatch(2))
	expectStatus(t, rec, http.StatusNoContent)

	if n := countRows(t, f, `SELECT COUNT(*) FROM task_definitions WHERE id = ?`, defID); n != 0 {
		t.Fatalf("definition rows = %d after delete, want 0", n)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM task_occurrences WHERE task_definition_id = ?`, defID); n != 0 {
		t.Fatalf("occurrence rows = %d after delete, want 0", n)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM task_occurrence_assignees oa
		JOIN task_occurrences o ON o.id = oa.task_occurrence_id WHERE o.task_definition_id = ?`, defID); n != 0 {
		t.Fatalf("copy rows = %d after delete, want 0", n)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM points_ledger WHERE reference_id = ?`, created.ID); n != 1 {
		t.Fatalf("ledger rows for the task = %d after delete, want 1 (points are kept)", n)
	}
	if n := countAudit(t, f, "task.deleted", defID); n != 1 {
		t.Fatalf("task.deleted audit rows = %d, want 1", n)
	}
	expectStatus(t, f.do(t, f.parent, http.MethodGet, deleteTaskPath(created.ID), nil, nil), http.StatusNotFound)
}

func TestDeleteRepeatingTaskRemovesEveryCopy(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	created := createRepeat(t, f, f.parent, "Walk dog", "2026-10-09", f.parent.memberID, child.memberID)
	defID := definitionOf(t, f, created.FirstTask.ID)

	expectStatus(t, f.do(t, f.parent, http.MethodPost, tasksPath+"/"+created.FirstTask.ID+"/archive", nil, map[string]string{"If-Match": `"1"`}), http.StatusOK)
	expectStatus(t, f.do(t, f.parent, http.MethodDelete, deleteTaskPath(created.FirstTask.ID), nil, ifMatch(2)), http.StatusNoContent)

	if n := countRows(t, f, `SELECT COUNT(*) FROM task_definitions WHERE id = ?`, defID); n != 0 {
		t.Fatalf("definition rows = %d after delete, want 0", n)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM task_occurrences WHERE task_definition_id = ?`, defID); n != 0 {
		t.Fatalf("occurrence rows = %d after delete, want 0 (every day's copy is gone)", n)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM task_definition_assignees WHERE task_definition_id = ?`, defID); n != 0 {
		t.Fatalf("definition assignee rows = %d after delete, want 0", n)
	}
}

func TestDeleteActiveTaskIsRefused(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))

	expectError(t, f.do(t, f.parent, http.MethodDelete, deleteTaskPath(created.ID), nil, ifMatch(1)), http.StatusConflict, "task_not_archived")

	if n := countRows(t, f, `SELECT COUNT(*) FROM task_definitions WHERE status = 'active'`); n != 1 {
		t.Fatalf("active definitions = %d after refused delete, want 1", n)
	}
}

func TestDeleteNeedsCurrentVersion(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	expectStatus(t, f.do(t, f.parent, http.MethodPost, tasksPath+"/"+created.ID+"/archive", nil, map[string]string{"If-Match": `"1"`}), http.StatusOK)

	expectStatus(t, f.do(t, f.parent, http.MethodDelete, deleteTaskPath(created.ID), nil, ifMatch(1)), http.StatusPreconditionFailed)
	if n := countRows(t, f, `SELECT COUNT(*) FROM task_definitions`); n != 1 {
		t.Fatalf("definitions = %d after stale delete, want 1", n)
	}
}

func TestDeleteUnknownTaskIsNotFound(t *testing.T) {
	f := newFixture(t)
	expectStatus(t, f.do(t, f.parent, http.MethodDelete, deleteTaskPath("00000000-0000-4000-8000-000000000000"), nil, ifMatch(1)), http.StatusNotFound)
}
