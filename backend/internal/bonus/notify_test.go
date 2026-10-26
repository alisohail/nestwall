package bonus

import (
	"context"
	"strings"
	"testing"
)

type noticeRow struct {
	RecipientID string
	Kind        string
	Body        string
}

// bonusNotices returns every notification written in the test family.
func bonusNotices(t *testing.T, e env) []noticeRow {
	t.Helper()
	rows, err := e.db.Query(`SELECT recipient_id, kind, body FROM notifications WHERE family_id = ?`, e.f.FamilyID)
	if err != nil {
		t.Fatalf("query notifications: %v", err)
	}
	defer rows.Close()
	var out []noticeRow
	for rows.Next() {
		var r noticeRow
		if err := rows.Scan(&r.RecipientID, &r.Kind, &r.Body); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("notifications rows: %v", err)
	}
	return out
}

func kindFor(rows []noticeRow, recipient, kind string) []noticeRow {
	var out []noticeRow
	for _, r := range rows {
		if r.RecipientID == recipient && r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func TestBonusSubmitApproveRejectNotices(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	task := publish(t, e, basicInput(kid.MemberID))
	ctx := context.Background()

	c, err := Grab(ctx, e.db, fixedClock(base), kid, task.ID)
	if err != nil {
		t.Fatalf("grab: %v", err)
	}
	if _, err := Complete(ctx, e.db, fixedClock(base), kid, task.ID, -1); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got := kindFor(bonusNotices(t, e), e.f.MemberID, "bonus_submitted"); len(got) != 1 {
		t.Fatalf("parent bonus_submitted notices = %d, want 1", len(got))
	}
	if got := kindFor(bonusNotices(t, e), kid.MemberID, "bonus_submitted"); len(got) != 0 {
		t.Fatalf("child got %d bonus_submitted notices, want 0", len(got))
	}

	if _, err := Approve(ctx, e.db, fixedClock(base), e.parent, c.ID, -1); err != nil {
		t.Fatalf("approve: %v", err)
	}
	approved := kindFor(bonusNotices(t, e), kid.MemberID, "bonus_approved")
	if len(approved) != 1 || !strings.Contains(approved[0].Body, "10") {
		t.Fatalf("child bonus_approved notices = %+v, want one mentioning 10 points", approved)
	}

	other := publish(t, e, basicInput(kid.MemberID))
	rc, err := Grab(ctx, e.db, fixedClock(base), kid, other.ID)
	if err != nil {
		t.Fatalf("grab second task: %v", err)
	}
	if _, err := Complete(ctx, e.db, fixedClock(base), kid, other.ID, -1); err != nil {
		t.Fatalf("complete second task: %v", err)
	}
	if _, err := Reject(ctx, e.db, fixedClock(base), e.parent, rc.ID, -1, "  not finished  "); err != nil {
		t.Fatalf("reject: %v", err)
	}
	rejected := kindFor(bonusNotices(t, e), kid.MemberID, "bonus_rejected")
	if len(rejected) != 1 || !strings.Contains(rejected[0].Body, "not finished") {
		t.Fatalf("child bonus_rejected notices = %+v, want one with the note", rejected)
	}
}
