package calendar

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestEventDefaultsToFirstCalendarWhenOmitted(t *testing.T) {
	f := newFixture(t)
	def := listSources(t, f, f.parent)[0]

	created := f.createEvent(t, f.parent, weeklyBody(f.parent.memberID))
	if created.CalendarID != def.ID {
		t.Fatalf("calendar_id = %q, want the Default calendar %q", created.CalendarID, def.ID)
	}
}

func TestEventCreatedIntoChosenCalendar(t *testing.T) {
	f := newFixture(t)
	listSources(t, f, f.parent)
	work := createSource(t, f, f.parent, "Work", "#0D9488")

	body := weeklyBody(f.parent.memberID)
	body["calendar_id"] = work.ID
	created := f.createEvent(t, f.parent, body)
	if created.CalendarID != work.ID {
		t.Fatalf("calendar_id = %q, want %q", created.CalendarID, work.ID)
	}
	if n := countRows(t, f, "SELECT COUNT(*) FROM calendar_events WHERE id = ? AND source_id = ?", created.ID, work.ID); n != 1 {
		t.Fatalf("event not stored in Work")
	}
	if got := findSource(t, f, f.parent, work.ID); got.EventCount != 1 {
		t.Fatalf("event_count = %d, want 1", got.EventCount)
	}
}

func TestEventRejectsUnknownCalendar(t *testing.T) {
	f := newFixture(t)
	listSources(t, f, f.parent)
	foreign := createSource(t, f, f.other, "Other family", "#0D9488")

	for _, id := range []string{foreign.ID, "not-a-uuid"} {
		body := weeklyBody(f.parent.memberID)
		body["calendar_id"] = id
		expectError(t, f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/events", body, nil),
			http.StatusBadRequest, "validation_failed")
	}
}

func TestEventUpdateMovesToAnotherCalendar(t *testing.T) {
	f := newFixture(t)
	def := listSources(t, f, f.parent)[0]
	f.clk.now = f.clk.now.Add(time.Minute)
	work := createSource(t, f, f.parent, "Work", "#0D9488")
	created := f.createEvent(t, f.parent, weeklyBody(f.parent.memberID))
	if created.CalendarID != def.ID {
		t.Fatalf("setup: calendar_id = %q, want %q", created.CalendarID, def.ID)
	}

	body := weeklyBody(f.parent.memberID)
	body["calendar_id"] = work.ID
	ok := f.do(t, f.parent, http.MethodPut, "/api/v1/calendar/events/"+created.ID, body,
		map[string]string{"If-Match": `"1"`})
	expectStatus(t, ok, http.StatusOK)
	if got := decodeEvent(t, ok); got.CalendarID != work.ID {
		t.Fatalf("calendar_id after move = %q, want %q", got.CalendarID, work.ID)
	}
}

func TestEventUpdateWithoutCalendarKeepsCurrent(t *testing.T) {
	f := newFixture(t)
	listSources(t, f, f.parent)
	work := createSource(t, f, f.parent, "Work", "#0D9488")
	body := weeklyBody(f.parent.memberID)
	body["calendar_id"] = work.ID
	created := f.createEvent(t, f.parent, body)

	edit := weeklyBody(f.parent.memberID)
	edit["title"] = "Renamed"
	ok := f.do(t, f.parent, http.MethodPut, "/api/v1/calendar/events/"+created.ID, edit,
		map[string]string{"If-Match": `"1"`})
	expectStatus(t, ok, http.StatusOK)
	if got := decodeEvent(t, ok); got.CalendarID != work.ID {
		t.Fatalf("calendar_id after edit without calendar = %q, want it kept as %q", got.CalendarID, work.ID)
	}
}

func TestEventUpdateRejectsForeignCalendar(t *testing.T) {
	f := newFixture(t)
	created := f.createEvent(t, f.parent, weeklyBody(f.parent.memberID))
	foreign := createSource(t, f, f.other, "Other family", "#0D9488")

	body := weeklyBody(f.parent.memberID)
	body["calendar_id"] = foreign.ID
	expectError(t, f.do(t, f.parent, http.MethodPut, "/api/v1/calendar/events/"+created.ID, body,
		map[string]string{"If-Match": `"1"`}), http.StatusBadRequest, "validation_failed")
}

// linkedEvent makes an event that already sits on a linked calendar, as one added before linked
// calendars stopped accepting new events. It is created locally and then moved onto the link.
func linkedEvent(t *testing.T, f fixture) (importResponse, eventResponse) {
	t.Helper()
	allowLoopback(f)
	imp := createLinked(t, f, newLinkedFeed(t, termICS))
	created := f.createEvent(t, f.parent, weeklyBody(f.parent.memberID))
	if _, err := f.db.Exec(`UPDATE calendar_events SET import_id = ?, from_feed = 0 WHERE id = ?`, imp.ID, created.ID); err != nil {
		t.Fatalf("attach event to linked calendar: %v", err)
	}
	return imp, created
}

func TestEventCannotBeCreatedOnLinkedCalendar(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	imp := createLinked(t, f, newLinkedFeed(t, termICS))

	body := weeklyBody(f.parent.memberID)
	body["calendar_id"] = imp.ID
	expectError(t, f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/events", body, nil),
		http.StatusBadRequest, "validation_failed")
}

func TestEventCannotBeMovedOntoLinkedCalendar(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	imp := createLinked(t, f, newLinkedFeed(t, termICS))
	created := f.createEvent(t, f.parent, weeklyBody(f.parent.memberID))

	body := weeklyBody(f.parent.memberID)
	body["calendar_id"] = imp.ID
	expectError(t, f.do(t, f.parent, http.MethodPut, "/api/v1/calendar/events/"+created.ID, body,
		map[string]string{"If-Match": `"1"`}), http.StatusBadRequest, "validation_failed")
}

func TestSyncRemovesEventsMadeOnLinkedCalendar(t *testing.T) {
	f := newFixture(t)
	imp, created := linkedEvent(t, f)

	// An older parser forces a full re-parse, which replaces the feed's events. The synced calendar
	// takes no user events, so this one must go too.
	if _, err := f.db.Exec(`UPDATE calendar_imports SET parser_version = 0 WHERE id = ?`, imp.ID); err != nil {
		t.Fatalf("age parser version: %v", err)
	}
	if err := f.h.syncImportFeed(context.Background(), f.a.FamilyID, imp.ID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE id = ?`, created.ID); n != 0 {
		t.Fatal("an event on a linked calendar survived a sync")
	}
}

func TestEventOnLinkedCalendarCanBeEditedAndDeleted(t *testing.T) {
	f := newFixture(t)
	imp, created := linkedEvent(t, f)

	edit := weeklyBody(f.parent.memberID)
	edit["title"] = "Renamed"
	ok := f.do(t, f.parent, http.MethodPut, "/api/v1/calendar/events/"+created.ID, edit,
		map[string]string{"If-Match": `"1"`})
	expectStatus(t, ok, http.StatusOK)
	if got := decodeEvent(t, ok); got.CalendarID != imp.ID || got.Imported || got.Title != "Renamed" {
		t.Fatalf("after edit = %+v, want still on %s and editable", got, imp.ID)
	}
	expectStatus(t, f.do(t, f.parent, http.MethodDelete, "/api/v1/calendar/events/"+created.ID, nil,
		map[string]string{"If-Match": `"2"`}), http.StatusNoContent)
}

func TestDeletingLinkedCalendarKeepsEventsMadeOnIt(t *testing.T) {
	f := newFixture(t)
	imp, created := linkedEvent(t, f)

	if rec := f.do(t, f.parent, http.MethodDelete, "/api/v1/calendar/imports/"+imp.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete import status = %d, want 204", rec.Code)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE id = ? AND import_id IS NULL`, created.ID); n != 1 {
		t.Fatal("an event made on a deleted linked calendar should stay as a local event")
	}
}
