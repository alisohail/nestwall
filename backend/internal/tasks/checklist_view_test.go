package tasks

import (
	"net/http"
	"testing"
)

// TestGetTaskReturnsChecklistInOrder checks that the task view lists its checklist in sort order
// with each item's done state.
func TestGetTaskReturnsChecklistInOrder(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Pack bag", "2026-10-09", f.parent.memberID))
	second := addChecklistItem(t, f, created.ID, "Socks", 1)
	first := addChecklistItem(t, f, created.ID, "Books", 0)
	markChecklistDone(t, f, first, f.parent.memberID)

	rec := f.do(t, f.parent, http.MethodGet, tasksPath+"/"+created.ID, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())

	if len(got.Checklist) != 2 {
		t.Fatalf("checklist = %+v, want 2 items", got.Checklist)
	}
	if got.Checklist[0].ID != first || got.Checklist[0].Text != "Books" || !got.Checklist[0].Done {
		t.Fatalf("first item = %+v, want done Books", got.Checklist[0])
	}
	if got.Checklist[1].ID != second || got.Checklist[1].Text != "Socks" || got.Checklist[1].Done {
		t.Fatalf("second item = %+v, want open Socks", got.Checklist[1])
	}
}

// TestListTasksIncludesChecklist checks that each task in the list carries its checklist, so the
// approvals view can show the steps without a second request.
func TestListTasksIncludesChecklist(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Pack bag", "2026-10-09", f.parent.memberID))
	second := addChecklistItem(t, f, created.ID, "Socks", 1)
	first := addChecklistItem(t, f, created.ID, "Books", 0)
	markChecklistDone(t, f, first, f.parent.memberID)

	list := listTasks(t, f, f.parent, "")
	if len(list.Tasks) != 1 {
		t.Fatalf("list = %+v, want one task", list.Tasks)
	}
	got := list.Tasks[0].Checklist
	if len(got) != 2 || got[0].ID != first || !got[0].Done || got[1].ID != second || got[1].Done {
		t.Fatalf("list checklist = %+v, want done Books then open Socks", got)
	}
}

// markChecklistDone records a done step on one member's copy of the task, as a tick does.
func markChecklistDone(t *testing.T, f fixture, itemID, memberID string) {
	t.Helper()
	execSQL(t, f.db,
		`INSERT INTO task_checklist_completions (family_id, task_occurrence_assignee_id, task_checklist_item_id, completed_at, completed_by_member_id)
		 SELECT ci.family_id, oa.id, ci.id, strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'), ?
		 FROM task_checklist_items ci
		 JOIN task_occurrence_assignees oa ON oa.task_occurrence_id = ci.task_occurrence_id
		 WHERE ci.id = ? AND oa.family_member_id = ?`,
		memberID, itemID, memberID)
}
