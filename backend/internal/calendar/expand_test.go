package calendar

import (
	"testing"
	"time"

	_ "time/tzdata"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

func strPtr(s string) *string { return &s }

func TestExpandWeeklyKeepsLocalTimeAcrossDST(t *testing.T) {
	s := Series{
		ID: "e1", Title: "Standup", Zone: mustZone(t, "Europe/London"),
		StartDate: "2025-03-20", StartHour: 9, DurationMinutes: 30,
		Rule: "FREQ=WEEKLY;BYDAY=TH",
	}
	got, err := Expand(s, nil, utc(2025, 3, 20, 0, 0), utc(2025, 4, 11, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 occurrences, got %d: %+v", len(got), got)
	}
	// 27 March is still GMT; 3 April is BST (UTC+1), so 09:00 local is 08:00 UTC.
	if want := utc(2025, 3, 27, 9, 0); !got[1].StartsAt.Equal(want) {
		t.Errorf("27 Mar start = %v, want %v", got[1].StartsAt, want)
	}
	if want := utc(2025, 4, 3, 8, 0); !got[2].StartsAt.Equal(want) {
		t.Errorf("3 Apr start = %v, want %v", got[2].StartsAt, want)
	}
	if d := got[2].EndsAt.Sub(got[2].StartsAt); d != 30*time.Minute {
		t.Errorf("duration = %v, want 30m", d)
	}
	if got[2].OccurrenceDate != "2025-04-03" || !got[2].Recurring || got[2].Edited {
		t.Errorf("unexpected occurrence fields: %+v", got[2])
	}
}

func TestExpandCancelledOccurrenceInNewYork(t *testing.T) {
	s := Series{
		ID: "e2", Title: "Medication", Zone: mustZone(t, "America/New_York"),
		StartDate: "2025-03-08", StartHour: 7, StartMinute: 30, DurationMinutes: 15,
		Rule: "FREQ=DAILY;COUNT=4",
	}
	ex := []Exception{{OccurrenceDate: "2025-03-10", Cancelled: true}}
	got, err := Expand(s, ex, utc(2025, 3, 8, 0, 0), utc(2025, 3, 13, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 occurrences, got %d", len(got))
	}
	for _, o := range got {
		if o.OccurrenceDate == "2025-03-10" {
			t.Fatalf("cancelled occurrence returned: %+v", o)
		}
	}
	// 8 March is EST (UTC-5) and 9 March is EDT (UTC-4): 07:30 local is 12:30 and 11:30 UTC.
	if want := utc(2025, 3, 8, 12, 30); !got[0].StartsAt.Equal(want) {
		t.Errorf("8 Mar start = %v, want %v", got[0].StartsAt, want)
	}
	if want := utc(2025, 3, 9, 11, 30); !got[1].StartsAt.Equal(want) {
		t.Errorf("9 Mar start = %v, want %v", got[1].StartsAt, want)
	}
	if want := utc(2025, 3, 11, 11, 30); !got[2].StartsAt.Equal(want) {
		t.Errorf("11 Mar start = %v, want %v", got[2].StartsAt, want)
	}
}

func TestExpandEditMovedIntoWindowFromOutside(t *testing.T) {
	s := Series{
		ID: "e3", Title: "Club", Zone: time.UTC,
		StartDate: "2025-01-06", StartHour: 10, DurationMinutes: 60,
		Rule: "FREQ=WEEKLY;BYDAY=MO",
	}
	ex := []Exception{{
		OccurrenceDate: "2025-01-06", Title: "Club (moved)",
		StartsAt: utc(2025, 2, 3, 11, 0), EndsAt: utc(2025, 2, 3, 12, 0),
	}}
	got, err := Expand(s, ex, utc(2025, 2, 1, 0, 0), utc(2025, 2, 8, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var moved *Occurrence
	for i := range got {
		if got[i].Edited {
			moved = &got[i]
		}
	}
	if moved == nil {
		t.Fatalf("moved occurrence missing from window: %+v", got)
	}
	if moved.Title != "Club (moved)" || !moved.StartsAt.Equal(utc(2025, 2, 3, 11, 0)) {
		t.Errorf("moved occurrence = %+v", *moved)
	}
	if moved.OccurrenceDate != "2025-01-06" {
		t.Errorf("moved occurrence date = %s, want original 2025-01-06", moved.OccurrenceDate)
	}
}

func TestExpandEditMovedOutOfWindowIsDropped(t *testing.T) {
	s := Series{
		ID: "e4", Title: "Club", Zone: time.UTC,
		StartDate: "2025-01-06", StartHour: 10, DurationMinutes: 60,
		Rule: "FREQ=WEEKLY;BYDAY=MO",
	}
	ex := []Exception{{
		OccurrenceDate: "2025-02-03", Title: "Moved away",
		StartsAt: utc(2025, 3, 3, 11, 0), EndsAt: utc(2025, 3, 3, 12, 0),
	}}
	// The edit replaces the 3 Feb occurrence and moves it to 3 Mar, so the week of 3 Feb is empty.
	got, err := Expand(s, ex, utc(2025, 2, 1, 0, 0), utc(2025, 2, 8, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want no occurrences in the window, got %+v", got)
	}
	got, err = Expand(s, ex, utc(2025, 3, 1, 0, 0), utc(2025, 3, 8, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var edited []Occurrence
	for _, o := range got {
		if o.Edited {
			edited = append(edited, o)
		}
	}
	if len(edited) != 1 || !edited[0].StartsAt.Equal(utc(2025, 3, 3, 11, 0)) {
		t.Fatalf("moved occurrence should appear in March, got %+v", got)
	}
}

func TestExpandOrphanExceptionIgnored(t *testing.T) {
	s := Series{
		ID: "e5", Title: "Club", Zone: time.UTC,
		StartDate: "2025-01-06", StartHour: 10, DurationMinutes: 60,
		Rule: "FREQ=WEEKLY;BYDAY=MO",
	}
	// 7 January is a Tuesday, so it is not an occurrence of the series.
	ex := []Exception{{
		OccurrenceDate: "2025-01-07", Title: "Ghost",
		StartsAt: utc(2025, 2, 4, 11, 0), EndsAt: utc(2025, 2, 4, 12, 0),
	}}
	got, err := Expand(s, ex, utc(2025, 2, 1, 0, 0), utc(2025, 2, 8, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range got {
		if o.Title == "Ghost" {
			t.Fatalf("orphan exception returned: %+v", o)
		}
	}
}

func TestExpandAllDaySpanAcrossDST(t *testing.T) {
	s := Series{
		ID: "e6", Title: "Camp", Zone: mustZone(t, "Europe/London"),
		AllDay: true, StartDate: "2025-03-29", SpanDays: 1,
	}
	got, err := Expand(s, nil, utc(2025, 3, 1, 0, 0), utc(2025, 4, 30, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 occurrence, got %d", len(got))
	}
	// Start is local midnight on 29 March (GMT). End is local midnight on 31 March, which is BST,
	// so the span is 47 hours long, not 48.
	if want := utc(2025, 3, 29, 0, 0); !got[0].StartsAt.Equal(want) {
		t.Errorf("start = %v, want %v", got[0].StartsAt, want)
	}
	if want := utc(2025, 3, 30, 23, 0); !got[0].EndsAt.Equal(want) {
		t.Errorf("end = %v, want %v", got[0].EndsAt, want)
	}
	if !got[0].AllDay {
		t.Error("AllDay = false, want true")
	}
}

func TestExpandAllDayRepeatingLookbackIncludesSpanningOccurrence(t *testing.T) {
	s := Series{
		ID: "e7", Title: "Holiday", Zone: time.UTC,
		AllDay: true, StartDate: "2025-01-01", SpanDays: 2,
		Rule: "FREQ=WEEKLY;BYDAY=WE",
	}
	// The occurrence starting Wednesday 1 January 2025 runs to Friday 3 January. A window that
	// starts on Friday 3 January must still see it.
	got, err := Expand(s, nil, utc(2025, 1, 3, 0, 0), utc(2025, 1, 4, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].OccurrenceDate != "2025-01-01" {
		t.Fatalf("want the spanning occurrence of 1 Jan, got %+v", got)
	}
}

func TestExpandKolkataAndAuckland(t *testing.T) {
	kolkata := Series{
		ID: "e8", Title: "Call", Zone: mustZone(t, "Asia/Kolkata"),
		StartDate: "2025-05-01", StartHour: 8, StartMinute: 30, DurationMinutes: 45,
	}
	got, err := Expand(kolkata, nil, utc(2025, 4, 30, 0, 0), utc(2025, 5, 2, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].StartsAt.Equal(utc(2025, 5, 1, 3, 0)) {
		t.Fatalf("Kolkata start = %+v, want 03:00 UTC", got)
	}

	auckland := Series{
		ID: "e9", Title: "Run", Zone: mustZone(t, "Pacific/Auckland"),
		StartDate: "2025-09-27", StartHour: 8, DurationMinutes: 60,
		Rule: "FREQ=DAILY;COUNT=3",
	}
	got, err = Expand(auckland, nil, utc(2025, 9, 26, 0, 0), utc(2025, 9, 30, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 occurrences, got %d", len(got))
	}
	// 27 Sep is NZST (UTC+12): 08:00 local is 20:00 UTC on 26 Sep. 28 Sep is NZDT (UTC+13): 19:00 UTC on 27 Sep.
	if !got[0].StartsAt.Equal(utc(2025, 9, 26, 20, 0)) {
		t.Errorf("27 Sep start = %v", got[0].StartsAt)
	}
	if !got[1].StartsAt.Equal(utc(2025, 9, 27, 19, 0)) {
		t.Errorf("28 Sep start = %v", got[1].StartsAt)
	}
}

func TestExpandOverlapWindowEdges(t *testing.T) {
	s := Series{
		ID: "e10", Title: "Long", Zone: time.UTC,
		StartDate: "2025-06-10", StartHour: 22, DurationMinutes: 180,
	}
	// Starts 22:00 on 10 June and ends 01:00 on 11 June.
	got, err := Expand(s, nil, utc(2025, 6, 11, 0, 0), utc(2025, 6, 12, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("event crossing into the window should be returned, got %d", len(got))
	}
	// Ends exactly at the start of the window, so it does not overlap.
	got, err = Expand(s, nil, utc(2025, 6, 11, 1, 0), utc(2025, 6, 12, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("event ending at window start should be excluded, got %+v", got)
	}
}

func TestExpandEmptyWindowAndSingleEvent(t *testing.T) {
	s := Series{
		ID: "e11", Title: "Once", Zone: time.UTC,
		StartDate: "2025-07-01", StartHour: 12, DurationMinutes: 60, Description: strPtr("d"),
	}
	if got, _ := Expand(s, nil, utc(2025, 7, 2, 0, 0), utc(2025, 7, 1, 0, 0)); got != nil {
		t.Fatalf("reversed window returned %+v", got)
	}
	got, err := Expand(s, nil, utc(2025, 7, 1, 0, 0), utc(2025, 7, 2, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Recurring || got[0].Description == nil || *got[0].Description != "d" {
		t.Fatalf("single event = %+v", got)
	}
}
