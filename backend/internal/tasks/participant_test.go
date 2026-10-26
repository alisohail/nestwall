package tasks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
)

// TestParticipantFailureRollsBackApproval checks that a participant's error stops the approval.
// The copy stays submitted, no approved event is raised and nothing is audited. The points are
// therefore never lost, because the parent can approve again once the cause is fixed.
func TestParticipantFailureRollsBackApproval(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	submitted := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)
	events := recordEvents(f)

	errPoints := errors.New("points unavailable")
	f.h.Participate(func(_ context.Context, _ *sql.Tx, _ Event) error { return errPoints })

	rec := f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "approve"), memberBody(child.memberID), ifMatch(submitted.Version))
	expectStatus(t, rec, http.StatusInternalServerError)

	c := readCopy(t, f, tk.ID, child.memberID)
	if c.status != statusSubmitted || c.decidedAt != nil {
		t.Errorf("copy = %+v, want still submitted with no decision", c)
	}
	if len(*events) != 0 {
		t.Errorf("events = %+v, want none after a rollback", *events)
	}
	if n := countAudit(t, f, "task.approved", tk.ID); n != 0 {
		t.Errorf("audit task.approved rows = %d, want 0", n)
	}
}
