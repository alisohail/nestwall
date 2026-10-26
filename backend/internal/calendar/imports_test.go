package calendar

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// termICS has a weekly series with one excluded date and an all-day series.
var termICS = []byte(strings.Join([]string{
	"BEGIN:VCALENDAR",
	"VERSION:2.0",
	"PRODID:-//NestWall test//EN",
	"BEGIN:VEVENT",
	"UID:swim@test",
	"DTSTART;TZID=Europe/London:20261012T090000",
	"DTEND;TZID=Europe/London:20261012T100000",
	"RRULE:FREQ=WEEKLY;BYDAY=MO",
	"EXDATE;TZID=Europe/London:20261019T090000",
	"SUMMARY:Swimming",
	"END:VEVENT",
	"BEGIN:VEVENT",
	"UID:school@test",
	"DTSTART;VALUE=DATE:20261014",
	"DTEND;VALUE=DATE:20261016",
	"SUMMARY:School closed",
	"END:VEVENT",
	"END:VCALENDAR",
}, "\r\n") + "\r\n")

// uploadICS posts a multipart import as the actor. A nil content sends no file part.
func uploadICS(t *testing.T, f fixture, a actor, name, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	fields := url.Values{}
	if name != "" {
		fields.Set("name", name)
	}
	return uploadImport(t, f, a, fields, filename, content)
}

func decodeImport(t *testing.T, rec *httptest.ResponseRecorder) importResponse {
	t.Helper()
	var out importResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode import %q: %v", rec.Body.String(), err)
	}
	return out
}

func listImports(t *testing.T, f fixture, a actor) []importResponse {
	t.Helper()
	rec := f.do(t, a, http.MethodGet, "/api/v1/calendar/imports", nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var out struct {
		Items []importResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode imports %q: %v", rec.Body.String(), err)
	}
	return out.Items
}

func countRows(t *testing.T, f fixture, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func TestImportCreateListAndDeleteRemovesEverything(t *testing.T) {
	f := newFixture(t)

	rec := uploadICS(t, f, f.parent, "", "school-term.ics", termICS)
	expectStatus(t, rec, http.StatusCreated)
	imp := decodeImport(t, rec)
	if imp.Name != "school-term" || imp.EventCount != 2 || imp.SkippedCount != 0 {
		t.Fatalf("import = %+v", imp)
	}
	if imp.FileName == nil || *imp.FileName != "school-term.ics" {
		t.Fatalf("file name = %v", imp.FileName)
	}

	items := listImports(t, f, f.parent)
	if len(items) != 1 || items[0].ID != imp.ID || items[0].EventCount != 2 {
		t.Fatalf("list = %+v", items)
	}

	resp, _ := f.rangeGet(t, f.parent, "from=2026-10-10&to=2026-10-20")
	var swim, school *rangeItem
	for i := range resp.Items {
		it := &resp.Items[i]
		switch it.Title {
		case "Swimming":
			swim = it
		case "School closed":
			school = it
		}
		if it.CalendarID == nil || *it.CalendarID != imp.ID {
			t.Fatalf("item %q calendar_id = %v, want the import id", it.Title, it.CalendarID)
		}
		if it.CalendarName == nil || *it.CalendarName != "school-term" {
			t.Fatalf("item %q calendar_name = %v", it.Title, it.CalendarName)
		}
	}
	if swim == nil || school == nil {
		t.Fatalf("range titles = %v, want Swimming and School closed", rangeTitles(resp.Items))
	}
	if swim.OccurrenceDate != "2026-10-12" {
		t.Fatalf("swim occurrence = %s; the 19th should be excluded", swim.OccurrenceDate)
	}
	for _, it := range resp.Items {
		if it.Title == "Swimming" && it.OccurrenceDate == "2026-10-19" {
			t.Fatal("excluded occurrence 2026-10-19 is still in the range")
		}
	}

	byImport, _ := f.rangeGet(t, f.parent, "from=2026-10-10&to=2026-10-20&calendar_id="+imp.ID)
	if len(byImport.Items) != len(resp.Items) {
		t.Fatalf("calendar filter items = %d, want %d", len(byImport.Items), len(resp.Items))
	}

	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_event_exceptions WHERE family_id = ?`, f.a.FamilyID); n != 1 {
		t.Fatalf("exceptions before delete = %d, want 1", n)
	}

	if rec := f.do(t, f.parent, http.MethodDelete, "/api/v1/calendar/imports/"+imp.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; body %s", rec.Code, rec.Body.String())
	}
	if items := listImports(t, f, f.parent); len(items) != 0 {
		t.Fatalf("list after delete = %+v", items)
	}
	after, _ := f.rangeGet(t, f.parent, "from=2026-10-10&to=2026-10-20")
	if len(after.Items) != 0 {
		t.Fatalf("range after delete = %v", rangeTitles(after.Items))
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id IS NOT NULL`); n != 0 {
		t.Fatalf("imported events left = %d", n)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_event_exceptions WHERE family_id = ?`, f.a.FamilyID); n != 0 {
		t.Fatalf("exceptions left = %d", n)
	}
}

func TestImportedEventCanBeReadLikeLocalEvent(t *testing.T) {
	f := newFixture(t)
	expectStatus(t, uploadICS(t, f, f.parent, "Term", "term.ics", termICS), http.StatusCreated)

	resp, _ := f.rangeGet(t, f.parent, "from=2026-10-14&to=2026-10-14")
	if len(resp.Items) != 1 || resp.Items[0].EventID == nil {
		t.Fatalf("range = %+v", resp.Items)
	}
	rec := f.do(t, f.parent, http.MethodGet, "/api/v1/calendar/events/"+*resp.Items[0].EventID, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	if ev := decodeEvent(t, rec); ev.Title != "School closed" {
		t.Fatalf("event title = %q", ev.Title)
	}
}

func TestImportedEventIsReadOnly(t *testing.T) {
	f := newFixture(t)
	expectStatus(t, uploadICS(t, f, f.parent, "Term", "term.ics", termICS), http.StatusCreated)

	resp, _ := f.rangeGet(t, f.parent, "from=2026-10-12&to=2026-10-12")
	var eventID string
	for _, it := range resp.Items {
		if it.Title == "Swimming" && it.EventID != nil {
			eventID = *it.EventID
		}
	}
	if eventID == "" {
		t.Fatalf("range = %v, want Swimming", rangeTitles(resp.Items))
	}
	path := "/api/v1/calendar/events/" + eventID
	ifMatch := map[string]string{"If-Match": `"1"`}

	ev := decodeEvent(t, f.do(t, f.parent, http.MethodGet, path, nil, nil))
	if !ev.Imported || ev.Version != 1 {
		t.Fatalf("event imported = %v version = %d, want true and 1", ev.Imported, ev.Version)
	}

	body := weeklyBody(f.parent.memberID)
	body["title"] = "Moved"
	expectError(t, f.do(t, f.parent, http.MethodPut, path, body, ifMatch), http.StatusConflict, "imported_event")
	expectError(t, f.do(t, f.parent, http.MethodDelete, path, nil, ifMatch), http.StatusConflict, "imported_event")

	occurrence := path + "/occurrences/2026-10-12"
	occBody := map[string]any{"title": "Moved", "start_date": "2026-10-12", "start_time": "10:00",
		"end_date": "2026-10-12", "end_time": "11:00"}
	expectError(t, f.do(t, f.parent, http.MethodPut, occurrence, occBody, ifMatch), http.StatusConflict, "imported_event")
	expectError(t, f.do(t, f.parent, http.MethodDelete, occurrence, nil, ifMatch), http.StatusConflict, "imported_event")

	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_event_exceptions WHERE event_id = ?`, eventID); n != 1 {
		t.Fatalf("exceptions = %d, want only the one from the feed", n)
	}
	after := decodeEvent(t, f.do(t, f.parent, http.MethodGet, path, nil, nil))
	if after.Title != "Swimming" || after.Version != 1 {
		t.Fatalf("event after refused changes = %q v%d, want Swimming v1", after.Title, after.Version)
	}
}

func TestImportMergesDuplicateEventsAndCountsThem(t *testing.T) {
	f := newFixture(t)
	copies := []byte(strings.Join([]string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//NestWall test//EN",
		"BEGIN:VEVENT",
		"UID:match@test",
		"DTSTART:20261012T140000Z",
		"DTEND:20261012T150000Z",
		"SUMMARY:Match (old)",
		"SEQUENCE:0",
		"END:VEVENT",
		"BEGIN:VEVENT",
		"UID:match@test",
		"DTSTART:20261012T140000Z",
		"DTEND:20261012T150000Z",
		"SUMMARY:Match (new)",
		"SEQUENCE:1",
		"END:VEVENT",
		"END:VCALENDAR",
	}, "\r\n") + "\r\n")

	rec := uploadICS(t, f, f.parent, "", "fixtures.ics", copies)
	expectStatus(t, rec, http.StatusCreated)
	imp := decodeImport(t, rec)
	if imp.EventCount != 1 || imp.DuplicateCount != 1 || imp.SkippedCount != 0 {
		t.Fatalf("import = %+v", imp)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ?`, imp.ID); n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}
	if items := listImports(t, f, f.parent); len(items) != 1 || items[0].DuplicateCount != 1 {
		t.Fatalf("list = %+v", items)
	}
}

func TestImportRejectsBadUploads(t *testing.T) {
	f := newFixture(t)
	tooLarge := bytes.Repeat([]byte("A"), maxImportBytes+1)
	longName := strings.Repeat("n", maxImportName+1)
	noEvents := []byte(strings.Join([]string{
		"BEGIN:VCALENDAR", "VERSION:2.0",
		"BEGIN:VEVENT", "UID:gone@test", "DTSTART:20261012T090000Z", "STATUS:CANCELLED", "END:VEVENT",
		"END:VCALENDAR",
	}, "\r\n") + "\r\n")

	tests := []struct {
		name   string
		rec    func(t *testing.T) *httptest.ResponseRecorder
		status int
		code   string
	}{
		{"missing file", func(t *testing.T) *httptest.ResponseRecorder {
			return uploadICS(t, f, f.parent, "Term", "", nil)
		}, http.StatusBadRequest, "missing_file"},
		{"not a calendar", func(t *testing.T) *httptest.ResponseRecorder {
			return uploadICS(t, f, f.parent, "", "notes.txt", []byte("hello"))
		}, http.StatusBadRequest, "invalid_ics"},
		{"no importable events", func(t *testing.T) *httptest.ResponseRecorder {
			return uploadICS(t, f, f.parent, "", "gone.ics", noEvents)
		}, http.StatusBadRequest, "no_events"},
		{"file too large", func(t *testing.T) *httptest.ResponseRecorder {
			return uploadICS(t, f, f.parent, "Big", "big.ics", tooLarge)
		}, http.StatusRequestEntityTooLarge, "file_too_large"},
		{"name too long", func(t *testing.T) *httptest.ResponseRecorder {
			return uploadICS(t, f, f.parent, longName, "term.ics", termICS)
		}, http.StatusBadRequest, "validation_failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expectError(t, tc.rec(t), tc.status, tc.code)
		})
	}

	if items := listImports(t, f, f.parent); len(items) != 0 {
		t.Fatalf("rejected uploads left imports behind: %+v", items)
	}
}

func TestImportAccessControl(t *testing.T) {
	f := newFixture(t)
	imp := decodeImport(t, uploadICS(t, f, f.parent, "Term", "term.ics", termICS))

	if rec := uploadICS(t, f, actor{}, "Term", "term.ics", termICS); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous upload status = %d, want 401", rec.Code)
	}
	if rec := uploadICS(t, f, f.kid, "Term", "term.ics", termICS); rec.Code != http.StatusForbidden {
		t.Fatalf("child upload status = %d, want 403", rec.Code)
	}
	if rec := f.do(t, f.kid, http.MethodDelete, "/api/v1/calendar/imports/"+imp.ID, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("child delete status = %d, want 403", rec.Code)
	}

	if items := listImports(t, f, f.other); len(items) != 0 {
		t.Fatalf("other family sees imports: %+v", items)
	}
	if rec := f.do(t, f.other, http.MethodDelete, "/api/v1/calendar/imports/"+imp.ID, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other family delete status = %d, want 404", rec.Code)
	}
	if rec := f.do(t, f.parent, http.MethodDelete, "/api/v1/calendar/imports/not-a-uuid", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id status = %d, want 404", rec.Code)
	}
	if items := listImports(t, f, f.parent); len(items) != 1 {
		t.Fatalf("import removed by another family or a bad id: %+v", items)
	}
}
