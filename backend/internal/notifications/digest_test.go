package notifications

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/email"
	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/platform/clock"
)

// digestFixture turns email on for the family and queues two notices for the parent.
func digestFixture(t *testing.T, digest bool) (fixture, *Digester) {
	t.Helper()
	f := newFixture(t)
	f.enableSMTP(t)
	if _, err := f.db.Exec(`UPDATE users SET email = 'parent@test.example' WHERE id = ?`, f.fam.ParentUserID); err != nil {
		t.Fatalf("set parent email: %v", err)
	}
	clk := clock.FakeClock{Time: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	s := notifypolicy.ShippedSettings()
	s.Email = true
	s.EmailDigest = digest
	if err := notifypolicy.Save(context.Background(), f.db, f.fam.FamilyID, s, clk.Now()); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	for _, body := range []string{"Ana finished the dishes", "Ben asked for a movie <night>"} {
		if _, err := notifypolicy.Notify(context.Background(), f.db, notifypolicy.Notice{
			FamilyID:    f.fam.FamilyID,
			RecipientID: f.fam.MemberID,
			Kind:        "task_submitted",
			Body:        body,
		}, clk.Now()); err != nil {
			t.Fatalf("notify: %v", err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	outbox := email.NewOutbox(f.db, clk, log, "test-key")
	return f, NewDigester(f.db, clk, log, outbox, "https://nestwall.test/")
}

type queued struct {
	to, subject, body string
	isHTML            bool
}

func queuedEmail(t *testing.T, f fixture) []queued {
	t.Helper()
	rows, err := f.db.Query(`SELECT to_address, subject, body, is_html FROM email_outbox WHERE family_id = ? ORDER BY created_at, id`, f.fam.FamilyID)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	var out []queued
	for rows.Next() {
		var q queued
		var html int
		if err := rows.Scan(&q.to, &q.subject, &q.body, &html); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		q.isHTML = html == 1
		out = append(out, q)
	}
	return out
}

func pendingCount(t *testing.T, f fixture) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE family_id = ? AND email_pending = 1`, f.fam.FamilyID).Scan(&n); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	return n
}

func TestDigestSendsOneEmailPerParent(t *testing.T) {
	f, d := digestFixture(t, true)
	sent, err := d.SendDue(context.Background())
	if err != nil || sent != 1 {
		t.Fatalf("SendDue = %d, %v; want 1 email", sent, err)
	}
	got := queuedEmail(t, f)
	if len(got) != 1 {
		t.Fatalf("queued = %d, want 1", len(got))
	}
	if got[0].subject != "NestWall: 2 new notifications" || !got[0].isHTML {
		t.Fatalf("subject=%q html=%v", got[0].subject, got[0].isHTML)
	}
	if !strings.Contains(got[0].body, "Ana finished the dishes") || !strings.Contains(got[0].body, "&lt;night&gt;") {
		t.Fatalf("digest body missing notices or not escaped: %s", got[0].body)
	}
	if !strings.Contains(got[0].body, `src="cid:nestwall-logo"`) {
		t.Fatalf("digest body missing the inline logo reference: %s", got[0].body)
	}
	if n := pendingCount(t, f); n != 0 {
		t.Fatalf("pending after send = %d, want 0", n)
	}
	if again, _ := d.SendDue(context.Background()); again != 0 {
		t.Fatalf("second run sent %d, want 0", again)
	}
}

func TestDigestOffSendsOneEmailPerNotice(t *testing.T) {
	f, d := digestFixture(t, false)
	sent, err := d.SendDue(context.Background())
	if err != nil || sent != 2 {
		t.Fatalf("SendDue = %d, %v; want 2 emails", sent, err)
	}
	got := queuedEmail(t, f)
	if len(got) != 2 {
		t.Fatalf("queued = %+v", got)
	}
	subjects := map[string]bool{got[0].subject: true, got[1].subject: true}
	if !subjects["NestWall: Ana finished the dishes"] || !subjects["NestWall: Ben asked for a movie <night>"] {
		t.Fatalf("subjects = %v, want one email per notice", subjects)
	}
}

// addNotice queues one notice for the parent at the given time.
func addNotice(t *testing.T, f fixture, body string, at time.Time) {
	t.Helper()
	if _, err := notifypolicy.Notify(context.Background(), f.db, notifypolicy.Notice{
		FamilyID: f.fam.FamilyID, RecipientID: f.fam.MemberID, Kind: "task_submitted", Body: body,
	}, at); err != nil {
		t.Fatalf("notify: %v", err)
	}
}

func fakeAt(h, m int) clock.FakeClock {
	return clock.FakeClock{Time: time.Date(2026, 10, 10, h, m, 0, 0, time.UTC)}
}

func TestDigestWaitsForFamilyInterval(t *testing.T) {
	f, d := digestFixture(t, true)
	ctx := context.Background()

	d.clock = fakeAt(9, 0)
	if sent, err := d.SendDue(ctx); err != nil || sent != 1 {
		t.Fatalf("first digest = %d, %v; want 1", sent, err)
	}

	addNotice(t, f, "Cara made her bed", fakeAt(9, 5).Now())
	d.clock = fakeAt(9, 14)
	if sent, _ := d.SendDue(ctx); sent != 0 {
		t.Fatalf("sent %d at 9:14, want 0 before the 15 minute interval", sent)
	}
	if n := pendingCount(t, f); n != 1 {
		t.Fatalf("pending before the interval = %d, want 1", n)
	}

	d.clock = fakeAt(9, 15)
	if sent, err := d.SendDue(ctx); err != nil || sent != 1 {
		t.Fatalf("digest at 9:15 = %d, %v; want 1", sent, err)
	}
	if n := pendingCount(t, f); n != 0 {
		t.Fatalf("pending after the interval = %d, want 0", n)
	}
}

func TestDigestUsesFamilyIntervalSetting(t *testing.T) {
	f, d := digestFixture(t, true)
	ctx := context.Background()
	if _, err := f.db.Exec(`UPDATE notification_settings SET digest_minutes = 60 WHERE family_id = ?`, f.fam.FamilyID); err != nil {
		t.Fatalf("set interval: %v", err)
	}

	d.clock = fakeAt(9, 0)
	if sent, _ := d.SendDue(ctx); sent != 1 {
		t.Fatalf("first digest = %d, want 1", sent)
	}
	addNotice(t, f, "Cara made her bed", fakeAt(9, 30).Now())
	d.clock = fakeAt(9, 30)
	if sent, _ := d.SendDue(ctx); sent != 0 {
		t.Fatalf("sent %d at 9:30, want 0 with a 60 minute interval", sent)
	}
	d.clock = fakeAt(10, 0)
	if sent, _ := d.SendDue(ctx); sent != 1 {
		t.Fatalf("sent %d at 10:00, want 1", sent)
	}
}

func TestDigestOffIgnoresInterval(t *testing.T) {
	f, d := digestFixture(t, false)
	ctx := context.Background()

	d.clock = fakeAt(9, 0)
	if sent, _ := d.SendDue(ctx); sent != 2 {
		t.Fatalf("first run = %d, want 2", sent)
	}
	addNotice(t, f, "Cara made her bed", fakeAt(9, 1).Now())
	d.clock = fakeAt(9, 1)
	if sent, _ := d.SendDue(ctx); sent != 1 {
		t.Fatalf("digest off sent %d one minute later, want 1 right away", sent)
	}
}
