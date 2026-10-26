package calendar

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

// childRoleID is the seeded Child role from migration 000001.
const childRoleID = "c3d4e5f6-a7b8-9012-cdef-123456789012"

// testClock is the clock the tests set. Handlers read it through clock.Clock.
type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

// fixtureNow is the time the fixture starts at. The family zone is UTC.
var fixtureNow = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

// testDBKey stands in for DB_KEY, which seals the links of external calendars.
const testDBKey = "test-db-key-for-calendar-links"

// actor is the signed-in member a request runs as. The cookie is a real session, so the
// permission middleware authenticates the request the way it does in the server.
type actor struct {
	memberID string
	userID   string
	cookie   *http.Cookie
}

// fixture holds a database with two families and the calendar routes wired the way the server
// wires them. Family A is the one the tests act as. kid is a child in family A.
type fixture struct {
	db     *sql.DB
	mux    *http.ServeMux
	clk    *testClock
	h      *Handlers
	a      testhelper.Family
	b      testhelper.Family
	parent actor
	other  actor
	kid    actor
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	conn := testhelper.OpenDB(t)
	a := testhelper.OneFamily(t, conn)
	b := testhelper.OneFamily(t, conn)
	clk := &testClock{now: fixtureNow}
	h := NewHandlers(conn, clk, slog.New(slog.NewTextHandler(io.Discard, nil)), testDBKey)
	manage := permissions.Middleware(conn, clk)
	read := permissions.Authenticated(conn, clk)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/calendar/events", manage(permissions.PermCalendarManage, h.CreateEvent))
	mux.HandleFunc("GET /api/v1/calendar/events/{id}", read(h.GetEvent))
	mux.HandleFunc("GET /api/v1/calendar/range", read(h.Range))
	mux.HandleFunc("PUT /api/v1/calendar/events/{id}", manage(permissions.PermCalendarManage, h.UpdateEvent))
	mux.HandleFunc("DELETE /api/v1/calendar/events/{id}", manage(permissions.PermCalendarManage, h.DeleteEvent))
	mux.HandleFunc("PUT /api/v1/calendar/events/{id}/occurrences/{date}",
		manage(permissions.PermCalendarManage, h.EditOccurrence))
	mux.HandleFunc("DELETE /api/v1/calendar/events/{id}/occurrences/{date}",
		manage(permissions.PermCalendarManage, h.CancelOccurrence))
	mux.HandleFunc("POST /api/v1/calendar/imports", manage(permissions.PermCalendarManage, h.CreateImport))
	mux.HandleFunc("GET /api/v1/calendar/imports", manage(permissions.PermCalendarManage, h.ListImports))
	mux.HandleFunc("PATCH /api/v1/calendar/imports/{id}", manage(permissions.PermCalendarManage, h.UpdateImport))
	mux.HandleFunc("DELETE /api/v1/calendar/imports/{id}", manage(permissions.PermCalendarManage, h.DeleteImport))
	mux.HandleFunc("POST /api/v1/calendar/imports/{id}/sync", manage(permissions.PermCalendarManage, h.SyncImport))
	mux.HandleFunc("GET /api/v1/calendar/sources", manage(permissions.PermCalendarManage, h.ListSources))
	mux.HandleFunc("POST /api/v1/calendar/sources", manage(permissions.PermCalendarManage, h.CreateSource))
	mux.HandleFunc("PATCH /api/v1/calendar/sources/{id}", manage(permissions.PermCalendarManage, h.UpdateSource))
	mux.HandleFunc("DELETE /api/v1/calendar/sources/{id}", manage(permissions.PermCalendarManage, h.DeleteSource))
	mux.HandleFunc("GET /api/v1/calendar/export.ics", read(h.ExportFamily))

	return fixture{
		db:     conn,
		mux:    mux,
		clk:    clk,
		h:      h,
		a:      a,
		b:      b,
		parent: actor{memberID: a.MemberID, userID: a.ParentUserID, cookie: a.SessionCookie},
		other:  actor{memberID: b.MemberID, userID: b.ParentUserID, cookie: b.SessionCookie},
		kid:    addChild(t, conn, clk, a.FamilyID),
	}
}

// addChild inserts an active child member with the Child role and a valid session.
func addChild(t *testing.T, conn *sql.DB, clk *testClock, familyID string) actor {
	t.Helper()
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin child: %v", err)
	}
	defer tx.Rollback()

	userID, memberID := ids.New(), ids.New()
	hash, err := auth.HashPassword("TestPassword1!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, ?, 'Kid', 1)`,
		userID, "kid_"+userID[:8], hash); err != nil {
		t.Fatalf("insert child user: %v", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour) VALUES (?, ?, ?, ?, 'active', '#F59E0B')`,
		memberID, familyID, userID, childRoleID); err != nil {
		t.Fatalf("insert child member: %v", err)
	}
	sess, err := auth.CreateSession(ctx, tx, clk, userID, "calendar-test")
	if err != nil {
		t.Fatalf("create child session: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit child: %v", err)
	}
	return actor{memberID: memberID, userID: userID,
		cookie: &http.Cookie{Name: auth.SessionCookieName, Value: sess.Token}}
}

func mustExec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// do sends a request as the given actor and returns the recorded response.
func (f fixture) do(t *testing.T, a actor, method, target string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if a.cookie != nil {
		req.AddCookie(a.cookie)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

// expectStatus fails the test when the response status is not want.
func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

// errorBody decodes the error envelope of a response.
func errorBody(t *testing.T, rec *httptest.ResponseRecorder) (code string, params map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code   string         `json:"code"`
			Params map[string]any `json:"params"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return env.Error.Code, env.Error.Params
}

// expectError checks the status and error code, and returns the error params.
func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	expectStatus(t, rec, status)
	got, params := errorBody(t, rec)
	if got != code {
		t.Fatalf("error code = %q, want %q; body: %s", got, code, rec.Body.String())
	}
	return params
}

// expectValidation checks a 400 validation_failed response for one field and reason.
func expectValidation(t *testing.T, rec *httptest.ResponseRecorder, field, reason string) {
	t.Helper()
	params := expectError(t, rec, http.StatusBadRequest, "validation_failed")
	if params["field"] != field || params["reason"] != reason {
		t.Fatalf("validation params = %v, want field %s reason %s", params, field, reason)
	}
}

func decodeEvent(t *testing.T, rec *httptest.ResponseRecorder) eventResponse {
	t.Helper()
	var out eventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode event %q: %v", rec.Body.String(), err)
	}
	return out
}

// weeklyBody is a Monday 09:00 to 10:00 London event that repeats weekly.
func weeklyBody(memberIDs ...string) map[string]any {
	if memberIDs == nil {
		memberIDs = []string{}
	}
	return map[string]any{
		"title":           "Dentist",
		"all_day":         false,
		"time_zone":       "Europe/London",
		"start_date":      "2026-10-12",
		"start_time":      "09:00",
		"end_date":        "2026-10-12",
		"end_time":        "10:00",
		"recurrence_rule": "FREQ=WEEKLY;BYDAY=MO",
		"member_ids":      memberIDs,
	}
}

// dailyBody is a New York 09:00 to 09:15 event that repeats daily from 10 October 2026.
func dailyBody(memberIDs ...string) map[string]any {
	if memberIDs == nil {
		memberIDs = []string{}
	}
	return map[string]any{
		"title":           "Pills",
		"all_day":         false,
		"time_zone":       "America/New_York",
		"start_date":      "2026-10-10",
		"start_time":      "09:00",
		"end_date":        "2026-10-10",
		"end_time":        "09:15",
		"recurrence_rule": "FREQ=DAILY",
		"member_ids":      memberIDs,
	}
}

// createEvent creates an event as the given actor and fails the test unless it is created.
func (f fixture) createEvent(t *testing.T, a actor, body map[string]any) eventResponse {
	t.Helper()
	rec := f.do(t, a, http.MethodPost, "/api/v1/calendar/events", body, nil)
	expectStatus(t, rec, http.StatusCreated)
	return decodeEvent(t, rec)
}

func (f fixture) get(t *testing.T, a actor, id string) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, a, http.MethodGet, "/api/v1/calendar/events/"+id, nil, nil)
}

func TestCreateAndGetRoundTrip(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/events",
		weeklyBody(f.parent.memberID), nil)
	expectStatus(t, rec, http.StatusCreated)
	if got := rec.Header().Get("ETag"); got != `"1"` {
		t.Fatalf("ETag = %q, want \"1\"", got)
	}
	created := decodeEvent(t, rec)
	if created.StartDate != "2026-10-12" || created.StartTime == nil || *created.StartTime != "09:00" {
		t.Fatalf("start = %s %v, want 2026-10-12 09:00", created.StartDate, created.StartTime)
	}
	if created.EndTime == nil || *created.EndTime != "10:00" || created.TimeZone != "Europe/London" {
		t.Fatalf("end/zone = %v %s, want 10:00 Europe/London", created.EndTime, created.TimeZone)
	}
	if created.RecurrenceRule == nil || len(created.MemberIDs) != 1 {
		t.Fatalf("rule/members = %v %v, want a rule and one member", created.RecurrenceRule, created.MemberIDs)
	}

	got := decodeEvent(t, f.get(t, f.parent, created.ID))
	if got.ID != created.ID || got.Title != "Dentist" || got.Version != 1 || got.StartDate != "2026-10-12" {
		t.Fatalf("GET = %+v, want the created event at version 1", got)
	}
}

func TestCreateValidation(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name  string
		edit  func(map[string]any)
		field string
		why   string
	}{
		{"empty title", func(b map[string]any) { b["title"] = "" }, "title", "required"},
		{"end before start", func(b map[string]any) { b["end_time"] = "08:00" }, "end_time", "before_start"},
		{"unknown zone", func(b map[string]any) { b["time_zone"] = "Mars/Olympus" }, "time_zone", "invalid"},
		{"member from another family", func(b map[string]any) {
			b["member_ids"] = []string{f.other.memberID}
		}, "member_ids", "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := weeklyBody(f.parent.memberID)
			tc.edit(body)
			expectValidation(t, f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/events", body, nil),
				tc.field, tc.why)
		})
	}

	body := weeklyBody()
	body["recurrence_rule"] = "FREQ=HOURLY"
	params := expectError(t, f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/events", body, nil),
		http.StatusBadRequest, "validation_failed")
	if params["field"] != "recurrence_rule" {
		t.Fatalf("rule error field = %v, want recurrence_rule", params["field"])
	}
}

func TestUpdateIfMatch(t *testing.T) {
	f := newFixture(t)
	created := f.createEvent(t, f.parent, weeklyBody(f.parent.memberID))
	path := "/api/v1/calendar/events/" + created.ID

	body := weeklyBody(f.parent.memberID)
	body["title"] = "Dentist (moved)"

	stale := f.do(t, f.parent, http.MethodPut, path, body, map[string]string{"If-Match": `"9"`})
	expectError(t, stale, http.StatusPreconditionFailed, "version_conflict")

	bad := f.do(t, f.parent, http.MethodPut, path, body, map[string]string{"If-Match": "nope"})
	expectError(t, bad, http.StatusBadRequest, "bad_request")

	ok := f.do(t, f.parent, http.MethodPut, path, body, map[string]string{"If-Match": `"1"`})
	expectStatus(t, ok, http.StatusOK)
	if got := ok.Header().Get("ETag"); got != `"2"` {
		t.Fatalf("ETag after update = %q, want \"2\"", got)
	}
	if updated := decodeEvent(t, ok); updated.Title != "Dentist (moved)" || updated.Version != 2 {
		t.Fatalf("updated = %q v%d, want the new title at version 2", updated.Title, updated.Version)
	}
}

func TestFamilyIsolation(t *testing.T) {
	f := newFixture(t)
	created := f.createEvent(t, f.parent, weeklyBody(f.parent.memberID))
	path := "/api/v1/calendar/events/" + created.ID

	expectStatus(t, f.get(t, f.other, created.ID), http.StatusNotFound)
	expectStatus(t, f.do(t, f.other, http.MethodPut, path, weeklyBody(), nil), http.StatusNotFound)
	expectStatus(t, f.do(t, f.other, http.MethodDelete, path, nil, nil), http.StatusNotFound)
}

func TestChildSeesOnlyOwnEvents(t *testing.T) {
	f := newFixture(t)

	assigned := f.createEvent(t, f.parent, weeklyBody(f.parent.memberID, f.kid.memberID))
	mine := f.createEvent(t, f.parent, weeklyBody(f.parent.memberID))

	expectStatus(t, f.get(t, f.kid, assigned.ID), http.StatusOK)
	expectStatus(t, f.get(t, f.kid, mine.ID), http.StatusNotFound)

	rec := f.do(t, f.kid, http.MethodPost, "/api/v1/calendar/events", weeklyBody(f.kid.memberID), nil)
	expectError(t, rec, http.StatusForbidden, "forbidden")
}

func TestEditAndCancelOccurrence(t *testing.T) {
	f := newFixture(t)
	daily := f.createEvent(t, f.parent, dailyBody(f.parent.memberID))
	path := "/api/v1/calendar/events/" + daily.ID

	cancel := f.do(t, f.parent, http.MethodDelete, path+"/occurrences/2026-10-11", nil,
		map[string]string{"If-Match": `"1"`})
	expectStatus(t, cancel, http.StatusNoContent)
	if got := cancel.Header().Get("ETag"); got != `"2"` {
		t.Fatalf("ETag after cancel = %q, want \"2\"", got)
	}

	after := decodeEvent(t, f.get(t, f.parent, daily.ID))
	if len(after.Exceptions) != 1 || after.Exceptions[0].Kind != "cancelled" ||
		after.Exceptions[0].OccurrenceDate != "2026-10-11" {
		t.Fatalf("exceptions after cancel = %+v, want one cancelled 2026-10-11", after.Exceptions)
	}

	edit := map[string]any{
		"title":      "Pills (late)",
		"start_date": "2026-10-12",
		"start_time": "14:00",
		"end_date":   "2026-10-12",
		"end_time":   "14:15",
	}
	stale := f.do(t, f.parent, http.MethodPut, path+"/occurrences/2026-10-12", edit,
		map[string]string{"If-Match": `"1"`})
	expectError(t, stale, http.StatusPreconditionFailed, "version_conflict")

	edited := f.do(t, f.parent, http.MethodPut, path+"/occurrences/2026-10-12", edit,
		map[string]string{"If-Match": `"2"`})
	expectStatus(t, edited, http.StatusOK)
	result := decodeEvent(t, edited)
	if len(result.Exceptions) != 2 {
		t.Fatalf("exceptions after edit = %+v, want two", result.Exceptions)
	}
	var found bool
	for _, x := range result.Exceptions {
		if x.OccurrenceDate == "2026-10-12" {
			found = x.Kind == "edited" && x.Title != nil && *x.Title == "Pills (late)"
		}
	}
	if !found {
		t.Fatalf("edited occurrence missing or wrong: %+v", result.Exceptions)
	}

	bad := map[string]any{"title": "", "start_date": "2026-10-12", "start_time": "14:00",
		"end_date": "2026-10-12", "end_time": "14:15"}
	expectValidation(t, f.do(t, f.parent, http.MethodPut, path+"/occurrences/2026-10-13", bad, nil),
		"title", "required")
	expectValidation(t, f.do(t, f.parent, http.MethodDelete, path+"/occurrences/2026-13-40", nil, nil),
		"occurrence_date", "invalid")
	expectStatus(t, f.do(t, f.parent, http.MethodDelete, path+"/occurrences/2026-10-10x", nil, nil),
		http.StatusBadRequest)
}

func TestOccurrenceErrors(t *testing.T) {
	f := newFixture(t)

	weekly := f.createEvent(t, f.parent, weeklyBody(f.parent.memberID))
	notMonday := f.do(t, f.parent, http.MethodDelete,
		"/api/v1/calendar/events/"+weekly.ID+"/occurrences/2026-10-13", nil, nil)
	expectError(t, notMonday, http.StatusNotFound, "not_found")

	single := weeklyBody(f.parent.memberID)
	single["recurrence_rule"] = nil
	once := f.createEvent(t, f.parent, single)
	notRecurring := f.do(t, f.parent, http.MethodDelete,
		"/api/v1/calendar/events/"+once.ID+"/occurrences/2026-10-12", nil, nil)
	expectError(t, notRecurring, http.StatusConflict, "not_recurring")
}

func TestDeleteRemovesRows(t *testing.T) {
	f := newFixture(t)
	daily := f.createEvent(t, f.parent, dailyBody(f.parent.memberID, f.kid.memberID))
	path := "/api/v1/calendar/events/" + daily.ID

	cancel := f.do(t, f.parent, http.MethodDelete, path+"/occurrences/2026-10-11", nil,
		map[string]string{"If-Match": `"1"`})
	expectStatus(t, cancel, http.StatusNoContent)

	expectStatus(t, f.do(t, f.parent, http.MethodDelete, path, nil, map[string]string{"If-Match": `"1"`}),
		http.StatusPreconditionFailed)
	expectStatus(t, f.do(t, f.parent, http.MethodDelete, path, nil, map[string]string{"If-Match": `"2"`}),
		http.StatusNoContent)
	expectStatus(t, f.get(t, f.parent, daily.ID), http.StatusNotFound)

	for _, table := range []string{"calendar_event_members", "calendar_event_exceptions"} {
		var n int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE event_id = ?`, daily.ID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("%s rows after delete = %d, want 0", table, n)
		}
	}
}

// TestStoredSeriesExpandsAcrossDST reads a stored event back and expands it, so the stored
// local start must hold New York's 09:00 on both sides of the change to standard time.
func TestStoredSeriesExpandsAcrossDST(t *testing.T) {
	f := newFixture(t)
	body := dailyBody(f.parent.memberID)
	body["start_date"] = "2026-10-30"
	body["end_date"] = "2026-10-30"
	created := f.createEvent(t, f.parent, body)

	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	e, err := loadEvent(context.Background(), f.db, f.a.FamilyID, created.ID)
	if err != nil {
		t.Fatalf("load event: %v", err)
	}
	series, err := e.series()
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	occ, err := Expand(series, e.exceptionList(),
		time.Date(2026, 10, 30, 0, 0, 0, 0, ny), time.Date(2026, 11, 3, 0, 0, 0, 0, ny))
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	// The window is half-open, so 3 Nov is excluded: 30 Oct, 31 Oct, 1 Nov (fall back) and 2 Nov.
	if len(occ) != 4 {
		t.Fatalf("occurrences = %d, want 4 (30 Oct to 2 Nov)", len(occ))
	}
	for _, o := range occ {
		local := o.StartsAt.In(ny)
		if local.Hour() != 9 || local.Minute() != 0 {
			t.Fatalf("occurrence %s starts at %s local, want 09:00", o.OccurrenceDate, local.Format(time.RFC3339))
		}
		if got := o.EndsAt.Sub(o.StartsAt); got != 15*time.Minute {
			t.Fatalf("occurrence %s lasts %s, want 15m", o.OccurrenceDate, got)
		}
	}
}
