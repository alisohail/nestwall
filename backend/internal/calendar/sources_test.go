package calendar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListCreatesDefaultCalendarOnce(t *testing.T) {
	f := newFixture(t)
	list := listSources(t, f, f.parent)
	if len(list) != 1 || list[0].Name != "Default" {
		t.Fatalf("first list = %+v, want one Default calendar", list)
	}
	again := listSources(t, f, f.parent)
	if len(again) != 1 || again[0].ID != list[0].ID {
		t.Fatalf("second list = %+v, want the same Default calendar", again)
	}
}

func TestDefaultCalendarHasColour(t *testing.T) {
	f := newFixture(t)
	list := listSources(t, f, f.parent)
	if len(list) != 1 || list[0].Colour != defaultLocalColour {
		t.Fatalf("Default = %+v, want colour %s", list, defaultLocalColour)
	}
}

func TestCreateManualCalendar(t *testing.T) {
	f := newFixture(t)
	listSources(t, f, f.parent)
	rec := f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/sources",
		map[string]any{"name": "Work", "colour": "#0d9488"}, nil)
	expectStatus(t, rec, http.StatusCreated)
	created := decodeSourceBody(t, rec)
	if created.Name != "Work" || created.Colour != "#0D9488" {
		t.Fatalf("created = %+v, want Work #0D9488", created)
	}
	if got := listSources(t, f, f.parent); len(got) != 2 {
		t.Fatalf("list after create has %d calendars, want 2", len(got))
	}
}

func TestCreateCalendarRejectsBadValues(t *testing.T) {
	f := newFixture(t)
	cases := []map[string]any{
		{"name": "", "colour": "#0D9488"},
		{"name": "   ", "colour": "#0D9488"},
		{"name": "Work"},
		{"name": "Work", "colour": ""},
		{"name": "Work", "colour": "teal"},
		{"name": "Work", "colour": "#12345"},
	}
	for _, body := range cases {
		expectError(t, f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/sources", body, nil),
			http.StatusBadRequest, "validation_failed")
	}
	if got := listSources(t, f, f.parent); len(got) != 1 {
		t.Fatalf("rejected creates left %d calendars, want only Default", len(got))
	}
}

func TestRenameAndRecolourCalendar(t *testing.T) {
	f := newFixture(t)
	work := createSource(t, f, f.parent, "Work", "#0D9488")
	path := "/api/v1/calendar/sources/" + work.ID

	rec := f.do(t, f.parent, http.MethodPatch, path, map[string]any{"name": "Office", "colour": "#ff0000"}, nil)
	expectStatus(t, rec, http.StatusOK)
	out := decodeSourceBody(t, rec)
	if out.Name != "Office" || out.Colour != "#FF0000" {
		t.Fatalf("updated = %+v, want Office #FF0000", out)
	}

	for _, body := range []map[string]any{
		{"name": "", "colour": "#FF0000"},
		{"name": "Office", "colour": ""},
		{"name": "Office", "colour": "teal"},
	} {
		expectError(t, f.do(t, f.parent, http.MethodPatch, path, body, nil), http.StatusBadRequest, "validation_failed")
	}
	if got := findSource(t, f, f.parent, work.ID); got.Name != "Office" {
		t.Fatalf("rejected updates changed the name to %q", got.Name)
	}
}

func TestCalendarAccessControl(t *testing.T) {
	f := newFixture(t)
	work := createSource(t, f, f.parent, "Work", "#0D9488")
	path := "/api/v1/calendar/sources/" + work.ID

	expectError(t, f.do(t, f.kid, http.MethodPatch, path, map[string]any{"name": "Mine", "colour": "#0D9488"}, nil),
		http.StatusForbidden, "forbidden")
	expectError(t, f.do(t, f.kid, http.MethodDelete, path, nil, nil), http.StatusForbidden, "forbidden")

	expectError(t, f.do(t, f.parent, http.MethodPatch, "/api/v1/calendar/sources/not-a-uuid",
		map[string]any{"name": "Mine", "colour": "#0D9488"}, nil), http.StatusNotFound, "not_found")
	expectError(t, f.do(t, f.parent, http.MethodDelete, "/api/v1/calendar/sources/not-a-uuid", nil, nil),
		http.StatusNotFound, "not_found")

	foreign := createSource(t, f, f.other, "Other family", "#0D9488")
	foreignPath := "/api/v1/calendar/sources/" + foreign.ID
	expectError(t, f.do(t, f.parent, http.MethodPatch, foreignPath, map[string]any{"name": "Mine", "colour": "#0D9488"}, nil),
		http.StatusNotFound, "not_found")
	expectError(t, f.do(t, f.parent, http.MethodDelete, foreignPath, nil, nil), http.StatusNotFound, "not_found")
}

func TestDeleteCalendarRemovesItsEvents(t *testing.T) {
	f := newFixture(t)
	listSources(t, f, f.parent)
	work := createSource(t, f, f.parent, "Work", "#0D9488")

	body := timedBody("Standup", "2026-10-12", "09:00", "09:30", f.parent.memberID)
	body["calendar_id"] = work.ID
	expectStatus(t, f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/events", body, nil), http.StatusCreated)
	if n := countRows(t, f, "SELECT COUNT(*) FROM calendar_events WHERE source_id = ?", work.ID); n != 1 {
		t.Fatalf("events in Work before delete = %d, want 1", n)
	}

	expectStatus(t, f.do(t, f.parent, http.MethodDelete, "/api/v1/calendar/sources/"+work.ID, nil, nil),
		http.StatusNoContent)
	if n := countRows(t, f, "SELECT COUNT(*) FROM calendar_events WHERE source_id = ?", work.ID); n != 0 {
		t.Fatalf("events left after deleting Work = %d, want 0", n)
	}
	if n := countRows(t, f, "SELECT COUNT(*) FROM calendar_event_members WHERE family_member_id = ?", f.parent.memberID); n != 0 {
		t.Fatalf("member rows left after deleting Work = %d, want 0", n)
	}
	if got := listSources(t, f, f.parent); len(got) != 1 || got[0].Name != "Default" {
		t.Fatalf("list after delete = %+v, want only Default", got)
	}
}

func TestDeleteLastCalendarIsRefused(t *testing.T) {
	f := newFixture(t)
	only := listSources(t, f, f.parent)[0]
	expectError(t, f.do(t, f.parent, http.MethodDelete, "/api/v1/calendar/sources/"+only.ID, nil, nil),
		http.StatusConflict, "last_calendar")
	if got := listSources(t, f, f.parent); len(got) != 1 {
		t.Fatalf("refused delete still removed the calendar; list = %+v", got)
	}
}

func TestDeleteCalendarMovesImportedEvents(t *testing.T) {
	f := newFixture(t)
	imp := decodeImport(t, uploadICS(t, f, f.parent, "", "school-term.ics", termICS))
	defaultCal := listSources(t, f, f.parent)[0]
	work := createSource(t, f, f.parent, "Work", "#0D9488")

	expectStatus(t, f.do(t, f.parent, http.MethodDelete, "/api/v1/calendar/sources/"+defaultCal.ID, nil, nil),
		http.StatusNoContent)
	if n := countRows(t, f, "SELECT COUNT(*) FROM calendar_events WHERE import_id = ? AND source_id = ?",
		imp.ID, work.ID); n != imp.EventCount {
		t.Fatalf("imported events on Work = %d, want %d", n, imp.EventCount)
	}
	if items := listImports(t, f, f.parent); len(items) != 1 || items[0].EventCount != imp.EventCount {
		t.Fatalf("import after calendar delete = %+v, want it kept with its events", items)
	}
}

func listSources(t *testing.T, f fixture, a actor) []SourceResponse {
	t.Helper()
	rec := f.do(t, a, http.MethodGet, "/api/v1/calendar/sources", nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var out struct {
		Items []SourceResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode sources %q: %v", rec.Body.String(), err)
	}
	return out.Items
}

func findSource(t *testing.T, f fixture, a actor, id string) SourceResponse {
	t.Helper()
	for _, s := range listSources(t, f, a) {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("calendar %s not in list", id)
	return SourceResponse{}
}

func createSource(t *testing.T, f fixture, a actor, name, colour string) SourceResponse {
	t.Helper()
	rec := f.do(t, a, http.MethodPost, "/api/v1/calendar/sources", map[string]any{"name": name, "colour": colour}, nil)
	expectStatus(t, rec, http.StatusCreated)
	return decodeSourceBody(t, rec)
}

func decodeSourceBody(t *testing.T, rec *httptest.ResponseRecorder) SourceResponse {
	t.Helper()
	var src SourceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &src); err != nil {
		t.Fatalf("decode source %q: %v", rec.Body.String(), err)
	}
	return src
}

