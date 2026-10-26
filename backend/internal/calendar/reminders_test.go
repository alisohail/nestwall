package calendar

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// oneOffBody is a 12:00 to 13:00 London event on 10 October 2026, which is 11:00 UTC. The fixture
// clock starts at 09:00 UTC that day.
func oneOffBody(memberIDs ...string) map[string]any {
	body := weeklyBody(memberIDs...)
	body["recurrence_rule"] = nil
	body["start_date"] = "2026-10-10"
	body["end_date"] = "2026-10-10"
	body["start_time"] = "12:00"
	body["end_time"] = "13:00"
	return body
}

// sendAt moves the clock to at and delivers the reminders that are due. It returns how many were sent.
func sendAt(t *testing.T, f fixture, at time.Time) int {
	t.Helper()
	f.clk.now = at
	n, err := NewReminders(f.db, f.clk, quietLogger()).Send(context.Background())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	return n
}

func reminderNotices(t *testing.T, f fixture, memberID string) int {
	t.Helper()
	return countRows(t, f,
		`SELECT COUNT(*) FROM notifications WHERE kind = 'event_reminder' AND recipient_id = ?`, memberID)
}

func TestNormaliseRemindersSortsAndDropsRepeats(t *testing.T) {
	got, err := normaliseReminders([]int{60, 0, 60, 15})
	if err != nil {
		t.Fatalf("normaliseReminders: %v", err)
	}
	if want := []int{0, 15, 60}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got, err := normaliseReminders(nil); err != nil || len(got) != 0 {
		t.Fatalf("nil input: got %v, %v", got, err)
	}
}

func TestNormaliseRemindersRejectsNegative(t *testing.T) {
	if _, err := normaliseReminders([]int{-1}); err == nil {
		t.Fatalf("normaliseReminders accepted a reminder after the start")
	}
}

func TestNormaliseRemindersAcceptsAnyLengthAndTrigger(t *testing.T) {
	long := make([]int, 50)
	for i := range long {
		long[i] = i * 1000
	}
	got, err := normaliseReminders(long)
	if err != nil || len(got) != 50 {
		t.Fatalf("got %d reminders, err %v; want 50 with no cap", len(got), err)
	}
}

func TestParseTriggerMinutes(t *testing.T) {
	cases := []struct {
		value string
		want  int
		ok    bool
	}{
		{"-PT15M", 15, true},
		{"-P1D", 1440, true},
		{"-PT1H30M", 90, true},
		{"-P1DT2H", 1560, true},
		{"-P1W", 10080, true},
		{"-PT0S", 0, true},
		{"PT0S", 0, true},
		{"-PT30S", 0, true},
		{"PT15M", 0, false},
		{"20261012T080000Z", 0, false},
		{"-P5000W", 50400000, true},
		{"-P", 0, false},
		{"garbage", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := parseTriggerMinutes(c.value)
		if got != c.want || ok != c.ok {
			t.Errorf("parseTriggerMinutes(%q) = %d, %v; want %d, %v", c.value, got, ok, c.want, c.ok)
		}
	}
}

func TestParseICSAlarmsBecomeReminders(t *testing.T) {
	s := parseOne(t, wrapCalendar(vevent(
		"UID:dentist@test",
		"DTSTART;TZID=Europe/London:20261012T090000",
		"DTEND;TZID=Europe/London:20261012T103000",
		"SUMMARY:Dentist",
		"BEGIN:VALARM",
		"ACTION:DISPLAY",
		"TRIGGER:-PT15M",
		"END:VALARM",
		"BEGIN:VALARM",
		"ACTION:DISPLAY",
		"TRIGGER;RELATED=END:-PT5M",
		"END:VALARM",
		"BEGIN:VALARM",
		"ACTION:DISPLAY",
		"TRIGGER:20261012T080000Z",
		"END:VALARM",
		"BEGIN:VALARM",
		"ACTION:DISPLAY",
		"TRIGGER:-P1D",
		"END:VALARM",
	)))
	if want := []int{15, 1440}; !reflect.DeepEqual(s.Event.reminders, want) {
		t.Fatalf("reminders = %v, want %v", s.Event.reminders, want)
	}
}

func TestEventRemindersStoredAndPastOnesDropped(t *testing.T) {
	f := newFixture(t)
	body := oneOffBody(f.parent.memberID)
	// At 09:00 UTC the 10:30 and 11:00 UTC reminders are still ahead. The 09:00 and 08:00 ones have passed.
	body["reminders"] = []int{0, 30, 120, 180}
	created := f.createEvent(t, f.parent, body)
	if want := []int{0, 30}; !reflect.DeepEqual(created.Reminders, want) {
		t.Fatalf("reminders = %v, want %v", created.Reminders, want)
	}

	got := decodeEvent(t, f.get(t, f.parent, created.ID))
	if !reflect.DeepEqual(got.Reminders, []int{0, 30}) {
		t.Fatalf("read reminders = %v, want [0 30]", got.Reminders)
	}
}

func TestEventRejectsNegativeReminder(t *testing.T) {
	f := newFixture(t)
	body := oneOffBody(f.parent.memberID)
	body["reminders"] = []int{-5}
	expectError(t, f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/events", body, nil),
		http.StatusBadRequest, "validation_failed")
}

func TestReminderSentOnceWhenDue(t *testing.T) {
	f := newFixture(t)
	body := oneOffBody(f.parent.memberID)
	body["reminders"] = []int{60}
	f.createEvent(t, f.parent, body)

	if n := sendAt(t, f, time.Date(2026, 10, 10, 9, 30, 0, 0, time.UTC)); n != 0 {
		t.Fatalf("sent %d before the reminder time, want 0", n)
	}
	if n := sendAt(t, f, time.Date(2026, 10, 10, 10, 30, 0, 0, time.UTC)); n != 1 {
		t.Fatalf("sent %d at the reminder time, want 1", n)
	}
	if n := sendAt(t, f, time.Date(2026, 10, 10, 10, 40, 0, 0, time.UTC)); n != 0 {
		t.Fatalf("sent %d again, want 0", n)
	}
	if got := reminderNotices(t, f, f.parent.memberID); got != 1 {
		t.Fatalf("notices = %d, want 1", got)
	}
}

func TestReminderNotNotifiedAfterEventStarts(t *testing.T) {
	f := newFixture(t)
	body := oneOffBody(f.parent.memberID)
	body["reminders"] = []int{60}
	f.createEvent(t, f.parent, body)

	if n := sendAt(t, f, time.Date(2026, 10, 10, 11, 30, 0, 0, time.UTC)); n != 0 {
		t.Fatalf("sent %d after the start, want 0", n)
	}
}

func TestReminderNotSentAgainOnEditWithSameStart(t *testing.T) {
	f := newFixture(t)
	body := oneOffBody(f.parent.memberID)
	body["reminders"] = []int{60}
	created := f.createEvent(t, f.parent, body)
	if n := sendAt(t, f, time.Date(2026, 10, 10, 10, 30, 0, 0, time.UTC)); n != 1 {
		t.Fatalf("setup: sent %d, want 1", n)
	}

	f.clk.now = time.Date(2026, 10, 10, 10, 35, 0, 0, time.UTC)
	edit := oneOffBody(f.parent.memberID)
	edit["title"] = "Dentist check"
	edit["reminders"] = []int{60}
	expectStatus(t, f.do(t, f.parent, http.MethodPut, "/api/v1/calendar/events/"+created.ID, edit,
		map[string]string{"If-Match": `"1"`}), http.StatusOK)

	if n := sendAt(t, f, time.Date(2026, 10, 10, 10, 40, 0, 0, time.UTC)); n != 0 {
		t.Fatalf("sent %d after an unrelated edit, want 0", n)
	}
	if got := reminderNotices(t, f, f.parent.memberID); got != 1 {
		t.Fatalf("notices = %d, want 1", got)
	}
}

func TestReminderRearmsWhenStartMoves(t *testing.T) {
	f := newFixture(t)
	body := oneOffBody(f.parent.memberID)
	body["reminders"] = []int{60}
	created := f.createEvent(t, f.parent, body)
	if n := sendAt(t, f, time.Date(2026, 10, 10, 10, 30, 0, 0, time.UTC)); n != 1 {
		t.Fatalf("setup: sent %d, want 1", n)
	}

	// Moving the start to 13:00 London (12:00 UTC) puts the reminder at 11:00 UTC.
	f.clk.now = time.Date(2026, 10, 10, 10, 35, 0, 0, time.UTC)
	edit := oneOffBody(f.parent.memberID)
	edit["start_time"] = "13:00"
	edit["end_time"] = "14:00"
	edit["reminders"] = []int{60}
	expectStatus(t, f.do(t, f.parent, http.MethodPut, "/api/v1/calendar/events/"+created.ID, edit,
		map[string]string{"If-Match": `"1"`}), http.StatusOK)

	if n := sendAt(t, f, time.Date(2026, 10, 10, 10, 50, 0, 0, time.UTC)); n != 0 {
		t.Fatalf("sent %d before the new reminder time, want 0", n)
	}
	if n := sendAt(t, f, time.Date(2026, 10, 10, 11, 10, 0, 0, time.UTC)); n != 1 {
		t.Fatalf("sent %d at the new reminder time, want 1", n)
	}
}

func TestReminderRecipientsAreAssigneesAndCreator(t *testing.T) {
	f := newFixture(t)

	// Assigned to the child only, created by the parent: both get it.
	forKid := oneOffBody(f.kid.memberID)
	forKid["reminders"] = []int{60}
	f.createEvent(t, f.parent, forKid)

	// Assigned to both: each is told once.
	both := oneOffBody(f.parent.memberID, f.kid.memberID)
	both["reminders"] = []int{60}
	f.createEvent(t, f.parent, both)

	if n := sendAt(t, f, time.Date(2026, 10, 10, 10, 30, 0, 0, time.UTC)); n != 2 {
		t.Fatalf("sent %d reminders, want 2", n)
	}
	if got := reminderNotices(t, f, f.parent.memberID); got != 2 {
		t.Fatalf("creator notices = %d, want 2", got)
	}
	if got := reminderNotices(t, f, f.kid.memberID); got != 2 {
		t.Fatalf("assignee notices = %d, want 2", got)
	}
}

func TestReminderFamilyWideGoesToEveryActiveMember(t *testing.T) {
	f := newFixture(t)
	body := oneOffBody()
	body["reminders"] = []int{60}
	f.createEvent(t, f.parent, body)

	// Send counts occurrences, so one family-wide event is one delivery that notifies each member.
	if n := sendAt(t, f, time.Date(2026, 10, 10, 10, 30, 0, 0, time.UTC)); n != 1 {
		t.Fatalf("sent %d, want 1", n)
	}
	if got := reminderNotices(t, f, f.parent.memberID); got != 1 {
		t.Fatalf("parent notices = %d, want 1", got)
	}
	if got := reminderNotices(t, f, f.kid.memberID); got != 1 {
		t.Fatalf("child notices = %d, want 1", got)
	}
}

func TestRepeatingReminderSendsPerOccurrence(t *testing.T) {
	f := newFixture(t)
	body := weeklyBody(f.parent.memberID)
	body["reminders"] = []int{60}
	f.createEvent(t, f.parent, body)

	// Weekly on Mondays from 2026-10-12 at 09:00 London (08:00 UTC), so the 60 minute reminder fires at 07:00 UTC.
	first := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	second := time.Date(2026, 10, 19, 7, 0, 0, 0, time.UTC)
	if n := sendAt(t, f, first.Add(-time.Minute)); n != 0 {
		t.Fatalf("sent %d before the first reminder, want 0", n)
	}
	if n := sendAt(t, f, first); n != 1 {
		t.Fatalf("first occurrence sent %d, want 1", n)
	}
	if n := sendAt(t, f, first.Add(5*time.Minute)); n != 0 {
		t.Fatalf("same occurrence resent %d, want 0", n)
	}
	if n := sendAt(t, f, second); n != 1 {
		t.Fatalf("second occurrence sent %d, want 1", n)
	}
	if got := reminderNotices(t, f, f.parent.memberID); got != 2 {
		t.Fatalf("notices = %d, want 2", got)
	}
}

func TestReminderDeliveriesRemovedWithEvent(t *testing.T) {
	f := newFixture(t)
	body := oneOffBody(f.parent.memberID)
	body["reminders"] = []int{60}
	created := f.createEvent(t, f.parent, body)
	if n := sendAt(t, f, time.Date(2026, 10, 10, 10, 30, 0, 0, time.UTC)); n != 1 {
		t.Fatalf("setup: sent %d, want 1", n)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_reminder_deliveries`); n != 1 {
		t.Fatalf("deliveries = %d, want 1", n)
	}

	expectStatus(t, f.do(t, f.parent, http.MethodDelete, "/api/v1/calendar/events/"+created.ID, nil,
		map[string]string{"If-Match": `"1"`}), http.StatusNoContent)
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_reminder_deliveries`); n != 0 {
		t.Fatalf("deliveries after delete = %d, want 0", n)
	}
}

func TestLinkedCalendarRefusesManualReminderEvents(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	imp := createLinked(t, f, newLinkedFeed(t, termICS))

	body := oneOffBody(f.parent.memberID)
	body["calendar_id"] = imp.ID
	body["reminders"] = []int{60}
	expectError(t, f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/events", body, nil),
		http.StatusBadRequest, "validation_failed")
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_event_reminders`); n != 0 {
		t.Fatalf("reminders stored for a refused event = %d, want 0", n)
	}
}

func TestReminderMarkedSentWhenInAppIsOff(t *testing.T) {
	f := newFixture(t)
	if _, err := f.db.Exec(
		`INSERT INTO notification_settings (family_id, in_app, browser, email, email_digest, digest_minutes, email_template, updated_at)
		 VALUES (?, 0, 1, 0, 0, 15, '', ?)`, f.a.FamilyID, dbTime(f.clk.now)); err != nil {
		t.Fatalf("turn off in-app: %v", err)
	}
	body := oneOffBody(f.parent.memberID)
	body["reminders"] = []int{60}
	f.createEvent(t, f.parent, body)

	if n := sendAt(t, f, time.Date(2026, 10, 10, 10, 30, 0, 0, time.UTC)); n != 1 {
		t.Fatalf("sent %d, want 1", n)
	}
	if got := reminderNotices(t, f, f.parent.memberID); got != 0 {
		t.Fatalf("notices = %d with in-app off, want 0", got)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_reminder_deliveries`); n != 1 {
		t.Fatalf("delivery rows = %d, want 1", n)
	}
}
