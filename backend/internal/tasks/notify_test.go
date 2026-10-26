package tasks

import (
	"context"
	"strings"
	"testing"

	"nestwall/backend/internal/notifypolicy"
)

// noticeRow is one notification as stored, for asserting on what a task action wrote.
type noticeRow struct {
	recipient string
	kind      string
	body      string
	pending   bool
}

func notices(t *testing.T, f fixture) []noticeRow {
	t.Helper()
	rows, err := f.db.Query(
		`SELECT recipient_id, kind, body, email_pending FROM notifications
		  WHERE family_id = ? ORDER BY created_at, id`, f.a.FamilyID)
	if err != nil {
		t.Fatalf("query notifications: %v", err)
	}
	defer rows.Close()
	var out []noticeRow
	for rows.Next() {
		var r noticeRow
		if err := rows.Scan(&r.recipient, &r.kind, &r.body, &r.pending); err != nil {
			t.Fatalf("scan notification: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("notifications rows: %v", err)
	}
	return out
}

func setNotifySettings(t *testing.T, f fixture, change func(*notifypolicy.Settings)) {
	t.Helper()
	s := notifypolicy.ShippedSettings()
	change(&s)
	if err := notifypolicy.Save(context.Background(), f.db, f.a.FamilyID, s, fixtureNow); err != nil {
		t.Fatalf("save notification settings: %v", err)
	}
}

func withNotifier(f fixture) {
	f.h.Participate(NewNotifier(f.clk).Apply)
}

func forRecipient(all []noticeRow, memberID string) []noticeRow {
	var out []noticeRow
	for _, n := range all {
		if n.recipient == memberID {
			out = append(out, n)
		}
	}
	return out
}

func TestApprovedNoticeGoesToChild(t *testing.T) {
	f := newFixture(t)
	withNotifier(f)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	submitted := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)
	for _, n := range notices(t, f) {
		if n.recipient == child.memberID {
			t.Fatalf("child got a notice on submit: %+v", n)
		}
	}

	runAction(t, f, f.parent, tk.ID, "approve", memberBody(child.memberID), submitted.Version)
	got := forRecipient(notices(t, f), child.memberID)
	if len(got) != 1 || got[0].kind != "task_approved" {
		t.Fatalf("child notices = %+v, want one task_approved", got)
	}
	if !strings.Contains(got[0].body, "approved") {
		t.Errorf("body = %q, want the approval wording", got[0].body)
	}
	if got[0].pending {
		t.Errorf("child notice marked for email, want in-app only")
	}
}

func TestRejectedNoticeGoesToChildWithNote(t *testing.T) {
	f := newFixture(t)
	withNotifier(f)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	submitted := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)

	runAction(t, f, f.parent, tk.ID, "reject",
		map[string]any{"member_id": child.memberID, "note": "Make the bed first"}, submitted.Version)
	got := forRecipient(notices(t, f), child.memberID)
	if len(got) != 1 || got[0].kind != "task_rejected" {
		t.Fatalf("child notices = %+v, want one task_rejected", got)
	}
}

func TestSubmittedNoticeGoesToParentsWithEmailWhenOn(t *testing.T) {
	f := newFixture(t)
	withNotifier(f)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	setNotifySettings(t, f, func(s *notifypolicy.Settings) { s.Email = true })

	runAction(t, f, child, tk.ID, "complete", nil, tk.Version)
	got := notices(t, f)
	if len(got) != 1 {
		t.Fatalf("notices = %+v, want one task_submitted for the parent", got)
	}
	if got[0].recipient != f.parent.memberID || got[0].kind != "task_submitted" {
		t.Errorf("notice = %+v, want task_submitted for the parent", got[0])
	}
	if !got[0].pending {
		t.Errorf("parent notice not marked for email with email on")
	}
	if !strings.Contains(got[0].body, "needs your approval") {
		t.Errorf("body = %q, want the approval request wording", got[0].body)
	}
}

func TestTaskNoticesWriteNothingWhenInAppOff(t *testing.T) {
	f := newFixture(t)
	withNotifier(f)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	setNotifySettings(t, f, func(s *notifypolicy.Settings) { s.InApp = false })

	submitted := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)
	runAction(t, f, f.parent, tk.ID, "approve", memberBody(child.memberID), submitted.Version)
	if n := notices(t, f); len(n) != 0 {
		t.Errorf("notices = %+v, want none while in-app is off", n)
	}
}
