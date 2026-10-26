package tasks

import (
	"net/http"
	"testing"
)

// TestChecklistTicksAreIndependentPerCopy checks that a step ticked on one assignee's copy leaves
// the other assignee's copy unchanged.
func TestChecklistTicksAreIndependentPerCopy(t *testing.T) {
	f := newFixture(t)
	ava := addChildMember(t, f, f.a.FamilyID)
	noah := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, ava.memberID, noah.memberID)
	first := addChecklistItem(t, f, tk.ID, "Bed made", 0)

	tickItem(t, f, ava, tk.ID, first, true, tk.Version)

	rec := f.do(t, noah, http.MethodGet, tasksPath+"/"+tk.ID, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeTask(t, rec.Body.Bytes()).Checklist; len(got) != 1 || got[0].Done {
		t.Fatalf("noah's copy = %+v, want Bed made open", got)
	}

	rec = f.do(t, f.parent, http.MethodGet, tasksPath+"/"+tk.ID+"?member_id="+ava.memberID, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeTask(t, rec.Body.Bytes()).Checklist; len(got) != 1 || !got[0].Done {
		t.Fatalf("ava's copy = %+v, want Bed made done", got)
	}
}

// TestParentCompletesChildCopyOnBehalf checks that a parent with tasks.manage can complete a
// child's copy, and that the child's copy is the one that changes.
func TestParentCompletesChildCopyOnBehalf(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)

	runAction(t, f, f.parent, tk.ID, "complete", memberBody(child.memberID), tk.Version)

	if c := readCopy(t, f, tk.ID, child.memberID); c.status != statusApproved {
		t.Errorf("child's copy = %+v, want approved", c)
	}
}

// TestChildCannotCompleteSiblingCopy checks that a child without tasks.manage cannot complete
// another member's copy.
func TestChildCannotCompleteSiblingCopy(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	sibling := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)

	rec := f.do(t, sibling, http.MethodPost, actionPath(tk.ID, "complete"), memberBody(child.memberID), ifMatch(tk.Version))
	expectStatus(t, rec, http.StatusForbidden)
	if c := readCopy(t, f, tk.ID, child.memberID); c.status == statusApproved {
		t.Errorf("child's copy = %+v, want unchanged", c)
	}
}
