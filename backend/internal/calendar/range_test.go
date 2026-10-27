package calendar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"nestwall/backend/internal/platform/ids"
)

// timedBody is a one-off event on date from start to end in UTC. Members are optional.
func timedBody(title, date, start, end string, members ...string) map[string]any {
	if members == nil {
		members = []string{}
	}
	return map[string]any{
		"title":      title,
		"all_day":    false,
		"time_zone":  "UTC",
		"start_date": date,
		"start_time": start,
		"end_date":   date,
		"end_time":   end,
		"member_ids": members,
	}
}

// insertTask adds a one-off task due at dueAt (a SQLite datetime) and assigns it to members. It
// returns the occurrence ID, which the calendar shows as task_id and the task pages load.
func insertTask(t *testing.T, f fixture, title, dueDate, dueAt string, members ...string) string {
	t.Helper()
	defID, occID := ids.New(), ids.New()
	mustExec(t, f.db,
		`INSERT INTO task_definitions (id, family_id, title, time_zone, starts_on, points, created_by)
		 VALUES (?, ?, ?, 'UTC', ?, 0, ?)`,
		defID, f.a.FamilyID, title, dueDate, f.parent.userID)
	mustExec(t, f.db,
		`INSERT INTO task_occurrences (id, family_id, task_definition_id, due_date, due_at) VALUES (?, ?, ?, ?, ?)`,
		occID, f.a.FamilyID, defID, dueDate, dueAt)
	for _, m := range members {
		mustExec(t, f.db,
			`INSERT INTO task_occurrence_assignees (id, family_id, task_occurrence_id, family_member_id)
			 VALUES (?, ?, ?, ?)`,
			ids.New(), f.a.FamilyID, occID, m)
	}
	return occID
}

// seedMixed adds the events and tasks the scope and filter tests read: a family dinner for everyone,
// a kid club for the child, the parent's daily pills and one task each.
func seedMixed(t *testing.T, f fixture) {
	t.Helper()
	f.createEvent(t, f.parent, timedBody("Family dinner", "2026-10-10", "18:00", "19:00"))
	f.createEvent(t, f.parent, timedBody("Kid club", "2026-10-10", "10:00", "11:00", f.kid.memberID))
	f.createEvent(t, f.parent, dailyBody(f.parent.memberID))
	insertTask(t, f, "Bins", "2026-10-11", "2026-10-11 17:00:00+00:00", f.parent.memberID)
	insertTask(t, f, "Homework", "2026-10-11", "2026-10-11 18:00:00+00:00", f.kid.memberID)
}

// rangeGet sends a range request as the actor and fails the test unless it succeeds.
func (f fixture) rangeGet(t *testing.T, a actor, query string) (rangeResponse, string) {
	t.Helper()
	rec := f.do(t, a, http.MethodGet, "/api/v1/calendar/range?"+query, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	return decodeRange(t, rec), rec.Body.String()
}

func decodeRange(t *testing.T, rec *httptest.ResponseRecorder) rangeResponse {
	t.Helper()
	var out rangeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode range %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestRangeCarriesEventReminders(t *testing.T) {
	f := newFixture(t)
	body := timedBody("Dentist", "2030-01-15", "09:00", "10:00")
	body["reminders"] = []int{15, 1440}
	f.createEvent(t, f.parent, body)
	f.createEvent(t, f.parent, timedBody("Kid club", "2030-01-15", "11:00", "12:00"))
	insertTask(t, f, "Bins", "2030-01-15", "2030-01-15 17:00:00+00:00", f.parent.memberID)

	resp, _ := f.rangeGet(t, f.parent, "from=2030-01-15&to=2030-01-16")
	got := map[string][]int{}
	for _, it := range resp.Items {
		got[it.Title] = it.Reminders
	}
	if want := []int{15, 1440}; !reflect.DeepEqual(got["Dentist"], want) {
		t.Fatalf("Dentist reminders = %v, want %v", got["Dentist"], want)
	}
	if len(got["Kid club"]) != 0 {
		t.Fatalf("Kid club reminders = %v, want none", got["Kid club"])
	}
}

func rangeTitles(items []rangeItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Title)
	}
	return out
}

func sortedTitles(items []rangeItem) []string {
	out := rangeTitles(items)
	sort.Strings(out)
	return out
}

func TestRangeListsEventsAndTasksInOrder(t *testing.T) {
	f := newFixture(t)
	f.createEvent(t, f.parent, dailyBody(f.parent.memberID))
	f.createEvent(t, f.parent, weeklyBody(f.parent.memberID))
	taskOcc := insertTask(t, f, "Bins", "2026-10-11", "2026-10-11 17:00:00+00:00", f.parent.memberID)

	resp, first := f.rangeGet(t, f.parent, "from=2026-10-10&to=2026-10-12")
	if resp.From != "2026-10-10" || resp.To != "2026-10-12" || resp.TimeZone != "UTC" {
		t.Fatalf("window = %s..%s %s, want 2026-10-10..2026-10-12 UTC", resp.From, resp.To, resp.TimeZone)
	}

	type want struct{ kind, title, starts string }
	expected := []want{
		{"event", "Pills", "2026-10-10T13:00:00Z"},
		{"event", "Pills", "2026-10-11T13:00:00Z"},
		{"task", "Bins", "2026-10-11T17:00:00Z"},
		{"event", "Dentist", "2026-10-12T08:00:00Z"},
		{"event", "Pills", "2026-10-12T13:00:00Z"},
	}
	if len(resp.Items) != len(expected) {
		t.Fatalf("got %d items, want %d: %+v", len(resp.Items), len(expected), resp.Items)
	}
	for i, w := range expected {
		it := resp.Items[i]
		if it.Kind != w.kind || it.Title != w.title || it.StartsAt != w.starts {
			t.Fatalf("item %d = %s %q at %s, want %s %q at %s",
				i, it.Kind, it.Title, it.StartsAt, w.kind, w.title, w.starts)
		}
	}

	task := resp.Items[2]
	if task.TaskID == nil || *task.TaskID != taskOcc || task.Status == nil || *task.Status != "scheduled" ||
		task.EventID != nil || !reflect.DeepEqual(task.MemberIDs, []string{f.parent.memberID}) {
		t.Fatalf("task item = %+v", task)
	}
	pills := resp.Items[0]
	if pills.EventID == nil || pills.Recurring == nil || !*pills.Recurring || pills.Edited == nil || *pills.Edited {
		t.Fatalf("event item = %+v, want a recurring unedited event", pills)
	}

	if _, again := f.rangeGet(t, f.parent, "from=2026-10-10&to=2026-10-12"); again != first {
		t.Fatalf("repeated range differs:\n%s\n%s", first, again)
	}
}

func TestRangeEditedAndCancelledOccurrences(t *testing.T) {
	f := newFixture(t)
	daily := f.createEvent(t, f.parent, dailyBody(f.parent.memberID))
	path := "/api/v1/calendar/events/" + daily.ID + "/occurrences/"

	expectStatus(t, f.do(t, f.parent, http.MethodDelete, path+"2026-10-11", nil,
		map[string]string{"If-Match": `"1"`}), http.StatusNoContent)
	expectStatus(t, f.do(t, f.parent, http.MethodPut, path+"2026-10-12", map[string]any{
		"title": "Pills (late)", "start_date": "2026-10-12", "start_time": "14:00",
		"end_date": "2026-10-12", "end_time": "14:15",
	}, map[string]string{"If-Match": `"2"`}), http.StatusOK)
	expectStatus(t, f.do(t, f.parent, http.MethodPut, path+"2026-10-13", map[string]any{
		"title": "Pills (moved)", "start_date": "2026-10-11", "start_time": "18:00",
		"end_date": "2026-10-11", "end_time": "18:15",
	}, map[string]string{"If-Match": `"3"`}), http.StatusOK)

	resp, _ := f.rangeGet(t, f.parent, "from=2026-10-10&to=2026-10-12")
	// The event is in New York time (EDT, UTC-4 in October), so 18:00 local is 22:00Z.
	type want struct {
		title, starts, date string
		edited              bool
	}
	expected := []want{
		{"Pills", "2026-10-10T13:00:00Z", "2026-10-10", false},
		{"Pills (moved)", "2026-10-11T22:00:00Z", "2026-10-13", true},
		{"Pills (late)", "2026-10-12T18:00:00Z", "2026-10-12", true},
	}
	if len(resp.Items) != len(expected) {
		t.Fatalf("got %d items, want %d: %+v", len(resp.Items), len(expected), resp.Items)
	}
	for i, w := range expected {
		it := resp.Items[i]
		if it.Title != w.title || it.StartsAt != w.starts || it.OccurrenceDate != w.date ||
			it.Edited == nil || *it.Edited != w.edited {
			t.Fatalf("item %d = %+v, want %+v", i, it, w)
		}
	}
}

func TestRangeIncludesOverlappingAllDayEvents(t *testing.T) {
	f := newFixture(t)
	f.createEvent(t, f.parent, map[string]any{
		"title": "Trip", "all_day": true, "time_zone": "UTC",
		"start_date": "2026-10-09", "end_date": "2026-10-11", "member_ids": []string{f.parent.memberID},
	})

	resp, _ := f.rangeGet(t, f.parent, "from=2026-10-11&to=2026-10-11")
	if got := rangeTitles(resp.Items); !reflect.DeepEqual(got, []string{"Trip"}) {
		t.Fatalf("on the last day = %v, want [Trip]", got)
	}
	resp, _ = f.rangeGet(t, f.parent, "from=2026-10-12&to=2026-10-12")
	if len(resp.Items) != 0 {
		t.Fatalf("after the last day = %+v, want none", resp.Items)
	}
}

func TestRangeUsesFamilyLocalDates(t *testing.T) {
	f := newFixture(t)
	mustExec(t, f.db, `UPDATE families SET timezone = 'America/New_York' WHERE id = ?`, f.a.FamilyID)
	body := timedBody("Late", "2026-10-10", "23:30", "23:45", f.parent.memberID)
	body["time_zone"] = "America/New_York"
	f.createEvent(t, f.parent, body)

	resp, _ := f.rangeGet(t, f.parent, "from=2026-10-10&to=2026-10-10")
	if resp.TimeZone != "America/New_York" || len(resp.Items) != 1 || resp.Items[0].Title != "Late" ||
		resp.Items[0].StartsAt != "2026-10-11T03:30:00Z" {
		t.Fatalf("local 10 Oct = %s %+v, want the 23:30 event at 2026-10-11T03:30:00Z", resp.TimeZone, resp.Items)
	}
	resp, _ = f.rangeGet(t, f.parent, "from=2026-10-11&to=2026-10-11")
	if len(resp.Items) != 0 {
		t.Fatalf("local 11 Oct = %+v, want none", resp.Items)
	}
}

func TestRangeChildSeesOnlyOwnItems(t *testing.T) {
	f := newFixture(t)
	seedMixed(t, f)
	query := "from=2026-10-10&to=2026-10-11"

	kid, _ := f.rangeGet(t, f.kid, query)
	if got := sortedTitles(kid.Items); !reflect.DeepEqual(got, []string{"Homework", "Kid club"}) {
		t.Fatalf("child titles = %v, want [Homework Kid club]", got)
	}
	for _, it := range kid.Items {
		if it.Kind == "task" && !reflect.DeepEqual(it.MemberIDs, []string{f.kid.memberID}) {
			t.Fatalf("child task members = %v, want only the child", it.MemberIDs)
		}
	}

	parent, _ := f.rangeGet(t, f.parent, query)
	want := []string{"Bins", "Family dinner", "Homework", "Kid club", "Pills", "Pills"}
	if got := sortedTitles(parent.Items); !reflect.DeepEqual(got, want) {
		t.Fatalf("parent titles = %v, want %v", got, want)
	}
}

func TestRangeFilters(t *testing.T) {
	f := newFixture(t)
	seedMixed(t, f)
	query := "from=2026-10-10&to=2026-10-11"

	byMember, _ := f.rangeGet(t, f.parent, query+"&member_id="+f.kid.memberID)
	if got := sortedTitles(byMember.Items); !reflect.DeepEqual(got, []string{"Homework", "Kid club"}) {
		t.Fatalf("member filter = %v, want [Homework Kid club]", got)
	}

	var calendarID string
	if err := f.db.QueryRow(`SELECT id FROM calendar_sources WHERE family_id = ? AND kind = 'local'`,
		f.a.FamilyID).Scan(&calendarID); err != nil {
		t.Fatalf("local calendar: %v", err)
	}
	byCalendar, _ := f.rangeGet(t, f.parent, query+"&calendar_id="+calendarID)
	if got := sortedTitles(byCalendar.Items); !reflect.DeepEqual(got, []string{"Family dinner", "Kid club", "Pills", "Pills"}) {
		t.Fatalf("calendar filter = %v, want events only", got)
	}
	for _, it := range byCalendar.Items {
		if it.Kind != "event" {
			t.Fatalf("calendar filter returned a %s item: %+v", it.Kind, it)
		}
		if it.CalendarName == nil || *it.CalendarName != "Default" {
			t.Fatalf("calendar_name = %v, want Default", it.CalendarName)
		}
	}

	noTasks, _ := f.rangeGet(t, f.parent, query+"&tasks=false")
	for _, it := range noTasks.Items {
		if it.Kind == "task" {
			t.Fatalf("tasks=false returned a task: %+v", it)
		}
	}
	if len(noTasks.Items) != 4 {
		t.Fatalf("tasks=false gave %d items, want the 4 events", len(noTasks.Items))
	}

	both, _ := f.rangeGet(t, f.parent, query+"&member_id="+f.kid.memberID+"&tasks=false")
	if got := rangeTitles(both.Items); !reflect.DeepEqual(got, []string{"Kid club"}) {
		t.Fatalf("member filter without tasks = %v, want [Kid club]", got)
	}
}

func TestRangeCalendarsListEveryVisibleCalendar(t *testing.T) {
	f := newFixture(t)
	everyone := decodeImport(t, uploadICS(t, f, f.parent, "", "school-term.ics", termICS))
	grownUpsICS := []byte(strings.Join([]string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//NestWall test//EN",
		"BEGIN:VEVENT",
		"UID:visit@test",
		"DTSTART;VALUE=DATE:20261020",
		"DTEND;VALUE=DATE:20261021",
		"SUMMARY:Grandparents visit",
		"END:VEVENT",
		"END:VCALENDAR",
	}, "\r\n") + "\r\n")
	parents := decodeImport(t, uploadImport(t, f, f.parent,
		url.Values{"name": {"Grown-ups"}, "audience": {"parents"}}, "grown-ups.ics", grownUpsICS))

	// Early September is before every imported event starts, so only the calendar list can show them.
	query := "from=2026-09-01&to=2026-09-02"
	parent, _ := f.rangeGet(t, f.parent, query)
	if len(parent.Items) != 0 {
		t.Fatalf("september items = %v, want none", rangeTitles(parent.Items))
	}
	if got := rangeCalendarIDs(parent.Calendars); !reflect.DeepEqual(got, []string{parents.ID, everyone.ID}) {
		t.Fatalf("parent calendars = %+v, want Grown-ups then school-term", parent.Calendars)
	}
	if parent.Calendars[0].Name != "Grown-ups" || parent.Calendars[1].Name != "school-term" {
		t.Fatalf("parent calendar names = %+v", parent.Calendars)
	}

	kid, _ := f.rangeGet(t, f.kid, query)
	if got := rangeCalendarIDs(kid.Calendars); !reflect.DeepEqual(got, []string{everyone.ID}) {
		t.Fatalf("child calendars = %+v, want only school-term", kid.Calendars)
	}
}

func rangeCalendarIDs(calendars []rangeCalendar) []string {
	out := make([]string, 0, len(calendars))
	for _, c := range calendars {
		out = append(out, c.ID)
	}
	return out
}

func TestRangeValidation(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name, query, field, reason string
	}{
		{"missing from", "to=2026-10-12", "from", "required"},
		{"bad from", "from=2026-13-01&to=2026-10-12", "from", "invalid"},
		{"to before from", "from=2026-10-12&to=2026-10-10", "to", "before_from"},
		{"too long", "from=2026-01-01&to=2027-01-02", "to", "too_long"},
		{"bad member", "from=2026-10-10&to=2026-10-12&member_id=nope", "member_id", "invalid"},
		{"member from another family", "from=2026-10-10&to=2026-10-12&member_id=" + f.other.memberID, "member_id", "invalid"},
		{"unknown calendar", "from=2026-10-10&to=2026-10-12&calendar_id=" + ids.New(), "calendar_id", "invalid"},
		{"bad tasks flag", "from=2026-10-10&to=2026-10-12&tasks=maybe", "tasks", "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectValidation(t, f.do(t, f.parent, http.MethodGet, "/api/v1/calendar/range?"+tc.query, nil, nil),
				tc.field, tc.reason)
		})
	}

	expectStatus(t, f.do(t, f.parent, http.MethodGet,
		"/api/v1/calendar/range?from=2026-01-01&to=2027-01-01", nil, nil), http.StatusOK)
}

func TestRangeIsolatedByFamily(t *testing.T) {
	f := newFixture(t)
	seedMixed(t, f)

	resp, _ := f.rangeGet(t, f.other, "from=2026-10-10&to=2026-10-11")
	if len(resp.Items) != 0 {
		t.Fatalf("family B sees %+v", resp.Items)
	}
}
