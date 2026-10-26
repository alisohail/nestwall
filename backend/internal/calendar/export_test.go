package calendar

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func exportRequest(t *testing.T, f fixture, a actor) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, a, http.MethodGet, "/api/v1/calendar/export.ics", nil, nil)
}

func findSeries(t *testing.T, got parsedImport, title string) importedSeries {
	t.Helper()
	for _, s := range got.Series {
		if s.Event.title == title {
			return s
		}
	}
	t.Fatalf("no series titled %q in %+v", title, got.Series)
	return importedSeries{}
}

func strOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func TestExportRoundTripKeepsEvents(t *testing.T) {
	f := newFixture(t)
	const dentistTitle = "Dentist, Dr; Lee \\ Ops"
	f.createEvent(t, f.parent, map[string]any{
		"title":           dentistTitle,
		"all_day":         false,
		"time_zone":       "Europe/London",
		"start_date":      "2026-10-12",
		"start_time":      "09:00",
		"end_date":        "2026-10-12",
		"end_time":        "10:00",
		"recurrence_rule": "FREQ=WEEKLY;BYDAY=MO",
		"location":        "Café High Street",
		"description":     "Bring the card. " + strings.Repeat("Long note ", 20) + "end",
		"member_ids":      []string{f.parent.memberID},
	})
	pills := f.createEvent(t, f.parent, dailyBody(f.parent.memberID))
	path := "/api/v1/calendar/events/" + pills.ID
	expectStatus(t, f.do(t, f.parent, http.MethodDelete, path+"/occurrences/2026-10-11", nil,
		map[string]string{"If-Match": `"1"`}), http.StatusNoContent)
	edit := map[string]any{
		"title":      "Pills (late)",
		"start_date": "2026-10-12",
		"start_time": "14:00",
		"end_date":   "2026-10-12",
		"end_time":   "14:15",
	}
	expectStatus(t, f.do(t, f.parent, http.MethodPut, path+"/occurrences/2026-10-12", edit,
		map[string]string{"If-Match": `"2"`}), http.StatusOK)
	f.createEvent(t, f.parent, map[string]any{
		"title":      "Trip",
		"all_day":    true,
		"time_zone":  "UTC",
		"start_date": "2026-10-20",
		"end_date":   "2026-10-21",
		"member_ids": []string{},
	})

	rec := exportRequest(t, f, f.parent)
	expectStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Fatalf("Content-Type = %q, want text/calendar", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="nestwall-family.ics"`) {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	body := rec.Body.String()
	for _, want := range []string{"BEGIN:VTIMEZONE", "TZID:Europe/London", "TZID:America/New_York",
		"BEGIN:DAYLIGHT", "TZOFFSETFROM:+0000", "TZOFFSETTO:+0100", "RRULE:FREQ=WEEKLY;BYDAY=MO"} {
		if !strings.Contains(body, want) {
			t.Fatalf("export missing %q:\n%s", want, body)
		}
	}

	got, err := parseICS(rec.Body.Bytes(), mustZone(t, "UTC"))
	if err != nil {
		t.Fatalf("parse export: %v", err)
	}
	if len(got.Series) != 3 || got.Skipped != 0 {
		t.Fatalf("series = %d skipped = %d, want 3 and 0", len(got.Series), got.Skipped)
	}

	dentist := findSeries(t, got, dentistTitle)
	v := dentist.Event
	if v.zone.String() != "Europe/London" || v.allDay {
		t.Fatalf("dentist zone/allDay = %s/%v", v.zone, v.allDay)
	}
	if v.timing.startDate != "2026-10-12" || v.timing.startTime != "09:00" ||
		v.timing.endTime != "10:00" {
		t.Fatalf("dentist timing = %+v", v.timing)
	}
	if v.rule == nil || *v.rule != "FREQ=WEEKLY;BYDAY=MO" {
		t.Fatalf("dentist rule = %v", v.rule)
	}
	if strOf(v.location) != "Café High Street" || !strings.HasPrefix(strOf(v.description), "Bring the card.") ||
		!strings.HasSuffix(strOf(v.description), "Long note end") {
		t.Fatalf("dentist location/description = %q / %q", strOf(v.location), strOf(v.description))
	}

	p := findSeries(t, got, "Pills")
	if p.Event.zone.String() != "America/New_York" || len(p.Cancelled) != 1 || p.Cancelled[0] != "2026-10-11" {
		t.Fatalf("pills zone/cancelled = %s/%v", p.Event.zone, p.Cancelled)
	}
	if len(p.Edited) != 1 || p.Edited[0].Date != "2026-10-12" {
		t.Fatalf("pills edited = %+v", p.Edited)
	}
	moved := p.Edited[0].Occurrence
	if moved.title != "Pills (late)" || moved.timing.startTime != "14:00" || moved.timing.endTime != "14:15" {
		t.Fatalf("moved occurrence = %+v", moved)
	}

	trip := findSeries(t, got, "Trip")
	if !trip.Event.allDay || trip.Event.timing.startDate != "2026-10-20" || trip.Event.timing.endDate != "2026-10-21" {
		t.Fatalf("trip = %+v", trip.Event)
	}
}

func TestExportEmptyFamilyIsValid(t *testing.T) {
	f := newFixture(t)
	rec := exportRequest(t, f, f.parent)
	expectStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.Contains(body, "BEGIN:VTIMEZONE") || strings.Contains(body, "BEGIN:VEVENT") {
		t.Fatalf("empty export = %q", body)
	}
	got, err := parseICS(rec.Body.Bytes(), mustZone(t, "UTC"))
	if err != nil || len(got.Series) != 0 {
		t.Fatalf("parse empty export = %+v, %v", got, err)
	}
}

func TestExportNeedsWholeCalendarAccess(t *testing.T) {
	f := newFixture(t)
	f.createEvent(t, f.parent, map[string]any{
		"title": "Family dinner", "all_day": true, "time_zone": "UTC",
		"start_date": "2026-10-22", "end_date": "2026-10-22", "member_ids": []string{},
	})
	expectError(t, exportRequest(t, f, f.kid), http.StatusForbidden, "forbidden")

	rec := exportRequest(t, f, f.other)
	expectStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "Family dinner") {
		t.Fatalf("other family exported this family's events")
	}
}

func TestFormatUTCOffset(t *testing.T) {
	cases := map[int]string{
		0:      "+0000",
		3600:   "+0100",
		-18000: "-0500",
		20700:  "+0545",
		-17762: "-045602",
	}
	for seconds, want := range cases {
		if got := formatUTCOffset(seconds); got != want {
			t.Fatalf("formatUTCOffset(%d) = %q, want %q", seconds, got, want)
		}
	}
}
