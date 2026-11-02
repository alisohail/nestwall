package tasks

import (
	"net/http"
	"testing"
)

func TestParentForcesOpenTaskMissed(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)

	rec := f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "miss"), nil, ifMatch(tk.Version))
	expectStatus(t, rec, http.StatusOK)
	if s := recScalar(t, f.db, `SELECT status FROM task_occurrences WHERE id = ?`, tk.ID); s != "missed" {
		t.Errorf("occurrence status = %s, want missed", s)
	}
	if c := readCopy(t, f, tk.ID, child.memberID); c.status != taskStatusToDo {
		t.Errorf("copy status = %s, want to_do left as it was", c.status)
	}
	if n := countAudit(t, f, "task.missed", tk.ID); n != 1 {
		t.Errorf("audit task.missed rows = %d, want 1", n)
	}

	missed := decodeTask(t, rec.Body.Bytes())
	rec = f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "miss"), nil, ifMatch(missed.Version))
	expectError(t, rec, http.StatusConflict, "task_not_open")
}

func TestParentCannotForceMissOnTaskWithNoOpenCopies(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)
	done := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)

	rec := f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "miss"), nil, ifMatch(done.Version))
	expectError(t, rec, http.StatusConflict, "task_not_open")
	if s := recScalar(t, f.db, `SELECT status FROM task_occurrences WHERE id = ?`, tk.ID); s == "missed" {
		t.Errorf("occurrence status = %s, want unchanged", s)
	}
}
