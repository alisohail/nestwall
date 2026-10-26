package rewards

import (
	"strings"
	"testing"
)

type redemptionNotice struct {
	RecipientID string
	Kind        string
	Body        string
}

func redemptionNotices(t *testing.T, f catFixture) []redemptionNotice {
	t.Helper()
	rows, err := f.db.Query(`SELECT recipient_id, kind, body FROM notifications WHERE family_id = ?`, f.fam.FamilyID)
	if err != nil {
		t.Fatalf("query notifications: %v", err)
	}
	defer rows.Close()
	var out []redemptionNotice
	for rows.Next() {
		var n redemptionNotice
		if err := rows.Scan(&n.RecipientID, &n.Kind, &n.Body); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("notifications rows: %v", err)
	}
	return out
}

func ofKind(all []redemptionNotice, recipient, kind string) []redemptionNotice {
	var out []redemptionNotice
	for _, n := range all {
		if n.RecipientID == recipient && n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

func TestRedemptionNoticesFollowTheDecision(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	rw := f.create(f.parent, `{"name":"Movie","cost":30,"approval_required":true}`)

	req, err := f.redeemAt(catNow, child, rw.ID, "k1")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	all := redemptionNotices(t, f)
	if got := ofKind(all, f.parent.memberID, "redemption_requested"); len(got) != 1 || !strings.Contains(got[0].Body, "Movie") {
		t.Fatalf("parent redemption_requested = %+v, want one naming the reward", got)
	}
	if got := ofKind(all, child.memberID, "redemption_requested"); len(got) != 0 {
		t.Fatalf("child got %d redemption_requested, want 0", len(got))
	}

	if _, err := f.approve(f.parent, req.ID, req.Version); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got := ofKind(redemptionNotices(t, f), child.memberID, "redemption_approved"); len(got) != 1 {
		t.Fatalf("child redemption_approved = %d, want 1", len(got))
	}

	second, err := f.redeemAt(catNow, child, rw.ID, "k2")
	if err != nil {
		t.Fatalf("second redeem: %v", err)
	}
	if _, err := f.reject(f.parent, second.ID, "  not this week  ", second.Version); err != nil {
		t.Fatalf("reject: %v", err)
	}
	rejected := ofKind(redemptionNotices(t, f), child.memberID, "redemption_rejected")
	if len(rejected) != 1 || !strings.Contains(rejected[0].Body, "not this week") {
		t.Fatalf("child redemption_rejected = %+v, want one with the reason", rejected)
	}
}
