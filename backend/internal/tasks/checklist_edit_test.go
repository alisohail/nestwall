package tasks

import (
	"net/http"
	"testing"

	"nestwall/backend/internal/platform/ids"
)

// checklistRoot returns the path that adds items to a task's checklist.
func checklistRoot(taskID string) string { return tasksPath + "/" + taskID + "/checklist" }

func TestChecklistAddRenameRemove(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)

	rec := f.do(t, f.parent, http.MethodPost, checklistRoot(tk.ID), map[string]any{"text": "  Bed made  "}, ifMatch(tk.Version))
	expectStatus(t, rec, http.StatusCreated)
	added := decodeTask(t, rec.Body.Bytes())
	if len(added.Checklist) != 1 || added.Checklist[0].Text != "Bed made" {
		t.Fatalf("checklist after add = %+v, want one item %q", added.Checklist, "Bed made")
	}
	if added.Version <= tk.Version {
		t.Fatalf("version after add = %d, want more than %d", added.Version, tk.Version)
	}

	rec = f.do(t, f.parent, http.MethodPost, checklistRoot(tk.ID), map[string]any{"text": "Teeth"}, ifMatch(added.Version))
	expectStatus(t, rec, http.StatusCreated)
	twoItems := decodeTask(t, rec.Body.Bytes())
	if len(twoItems.Checklist) != 2 || twoItems.Checklist[1].Text != "Teeth" {
		t.Fatalf("checklist order = %+v, want Teeth second", twoItems.Checklist)
	}

	first := twoItems.Checklist[0].ID
	rec = f.do(t, f.parent, http.MethodPatch, checklistPath(tk.ID, first), map[string]any{"text": "Bed tidy"}, ifMatch(twoItems.Version))
	expectStatus(t, rec, http.StatusOK)
	renamed := decodeTask(t, rec.Body.Bytes())
	if renamed.Checklist[0].Text != "Bed tidy" {
		t.Fatalf("renamed item = %q, want %q", renamed.Checklist[0].Text, "Bed tidy")
	}

	rec = f.do(t, f.parent, http.MethodDelete, checklistPath(tk.ID, first), nil, ifMatch(renamed.Version))
	expectStatus(t, rec, http.StatusOK)
	removed := decodeTask(t, rec.Body.Bytes())
	if len(removed.Checklist) != 1 || removed.Checklist[0].Text != "Teeth" {
		t.Fatalf("checklist after remove = %+v, want only Teeth", removed.Checklist)
	}
}

func TestChecklistRejectsBadTextAndUnknownItem(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)

	rec := f.do(t, f.parent, http.MethodPost, checklistRoot(tk.ID), map[string]any{"text": "   "}, ifMatch(tk.Version))
	expectError(t, rec, http.StatusBadRequest, "validation_failed")

	rec = f.do(t, f.parent, http.MethodPost, checklistRoot(tk.ID), map[string]any{}, ifMatch(tk.Version))
	expectError(t, rec, http.StatusBadRequest, "validation_failed")

	rec = f.do(t, f.parent, http.MethodDelete, checklistPath(tk.ID, ids.New()), nil, ifMatch(tk.Version))
	expectStatus(t, rec, http.StatusNotFound)
}

func TestChecklistLockedOnceAssigneeCompletes(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)

	rec := f.do(t, child, http.MethodPost, actionPath(tk.ID, "complete"), nil, ifMatch(tk.Version))
	expectStatus(t, rec, http.StatusOK)
	done := decodeTask(t, rec.Body.Bytes())

	rec = f.do(t, f.parent, http.MethodPost, checklistRoot(tk.ID), map[string]any{"text": "Late add"}, ifMatch(done.Version))
	expectError(t, rec, http.StatusConflict, "checklist_locked")
}

func TestParentTicksChildCopyOnBehalf(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	item := addChecklistItem(t, f, tk.ID, "Bed made", 0)

	ticked := f.do(t, f.parent, http.MethodPut, checklistPath(tk.ID, item),
		map[string]any{"done": true, "member_id": child.memberID}, ifMatch(tk.Version))
	expectStatus(t, ticked, http.StatusOK)
	if got := decodeTask(t, ticked.Body.Bytes()); len(got.Checklist) != 1 || !got.Checklist[0].Done {
		t.Fatalf("checklist = %+v, want the item done", got.Checklist)
	}

	version := decodeTask(t, ticked.Body.Bytes()).Version
	rec := f.do(t, child, http.MethodPut, checklistPath(tk.ID, item),
		map[string]any{"done": false, "member_id": f.parent.memberID}, ifMatch(version))
	expectError(t, rec, http.StatusForbidden, "forbidden")

	rec = f.do(t, f.parent, http.MethodPut, checklistPath(tk.ID, item),
		map[string]any{"done": false, "member_id": "not-a-uuid"}, ifMatch(version))
	expectError(t, rec, http.StatusBadRequest, "validation_failed")

	rec = f.do(t, f.parent, http.MethodPut, checklistPath(tk.ID, item),
		map[string]any{"done": false, "member_id": ids.New()}, ifMatch(version))
	expectStatus(t, rec, http.StatusNotFound)

	rec = f.do(t, child, http.MethodPost, actionPath(tk.ID, "complete"), nil, ifMatch(version))
	expectStatus(t, rec, http.StatusOK)
	submitted := decodeTask(t, rec.Body.Bytes())

	rec = f.do(t, f.parent, http.MethodPut, checklistPath(tk.ID, item),
		map[string]any{"done": false, "member_id": child.memberID}, ifMatch(submitted.Version))
	expectError(t, rec, http.StatusConflict, "checklist_locked")
}
