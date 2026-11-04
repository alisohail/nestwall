package tasks

import (
	"net/http"
	"testing"
)

func TestParentSkipsOpenTaskWithoutPenalty(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)

	rec := f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "miss"), nil, ifMatch(tk.Version))
	expectStatus(t, rec, http.StatusOK)
	if c := readCopy(t, f, tk.ID, child.memberID); c.status != statusSkipped {
		t.Errorf("copy status = %s, want skipped", c.status)
	}
	if s := recScalar(t, f.db, `SELECT status FROM task_occurrences WHERE id = ?`, tk.ID); s == "missed" {
		t.Errorf("occurrence status = %s, want it not forced to missed", s)
	}
	if n := countAudit(t, f, "task.skipped", tk.ID); n != 1 {
		t.Errorf("audit task.skipped rows = %d, want 1", n)
	}

	skipped := decodeTask(t, rec.Body.Bytes())
	rec = f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "miss"), nil, ifMatch(skipped.Version))
	expectError(t, rec, http.StatusConflict, "task_not_open")
}

func TestParentCannotSkipTaskWithNoOpenCopies(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)
	done := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)

	rec := f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "miss"), nil, ifMatch(done.Version))
	expectError(t, rec, http.StatusConflict, "task_not_open")
	if c := readCopy(t, f, tk.ID, child.memberID); c.status == statusSkipped {
		t.Errorf("copy status = %s, want unchanged", c.status)
	}
}
