package calendar

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// crlf joins ICS lines with CRLF, the line ending RFC 5545 requires.
func crlf(lines ...string) []byte {
	return []byte(strings.Join(lines, "\r\n") + "\r\n")
}

// wrapCalendar puts events between a VCALENDAR header and footer.
func wrapCalendar(events ...string) []byte {
	lines := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//NestWall test//EN"}
	for _, e := range events {
		lines = append(lines, e)
	}
	lines = append(lines, "END:VCALENDAR")
	return crlf(lines...)
}

// vevent wraps the given property lines in a VEVENT block.
func vevent(lines ...string) string {
	return strings.Join(append(append([]string{"BEGIN:VEVENT"}, lines...), "END:VEVENT"), "\r\n")
}

func parseOne(t *testing.T, data []byte) importedSeries {
	t.Helper()
	got, err := parseICS(data, mustZone(t, "UTC"))
	if err != nil {
		t.Fatalf("parseICS: %v", err)
	}
	if len(got.Series) != 1 {
		t.Fatalf("series = %d (skipped %d), want 1", len(got.Series), got.Skipped)
	}
	return got.Series[0]
}

func TestParseICSTimedWithZone(t *testing.T) {
	s := parseOne(t, wrapCalendar(vevent(
		"UID:dentist@test",
		"DTSTART;TZID=Europe/London:20261012T090000",
		"DTEND;TZID=Europe/London:20261012T103000",
		"SUMMARY:Dentist\\, Dr Lee",
		"LOCATION:High Street",
	)))
	v := s.Event
	if v.title != "Dentist, Dr Lee" || v.allDay {
		t.Fatalf("title/allDay = %q/%v", v.title, v.allDay)
	}
	if v.zone.String() != "Europe/London" {
		t.Fatalf("zone = %s", v.zone)
	}
	if v.timing.startDate != "2026-10-12" || v.timing.startTime != "09:00" ||
		v.timing.endDate != "2026-10-12" || v.timing.endTime != "10:30" {
		t.Fatalf("timing = %+v", v.timing)
	}
	if v.location == nil || *v.location != "High Street" {
		t.Fatalf("location = %v", v.location)
	}
	if v.rule != nil {
		t.Fatalf("rule = %v, want none", *v.rule)
	}
}

func TestParseICSKeepsUnescapedCommas(t *testing.T) {
	s := parseOne(t, wrapCalendar(vevent(
		"UID:sample@test",
		"DTSTART:20260915T090000",
		"DTEND:20260915T100000",
		"SUMMARY:Lunch, team",
		"LOCATION:Toronto, ON",
		"DESCRIPTION:Bring a\\, pen\\; paper",
	)))
	v := s.Event
	if v.title != "Lunch, team" {
		t.Fatalf("title = %q", v.title)
	}
	if v.location == nil || *v.location != "Toronto, ON" {
		t.Fatalf("location = %v", v.location)
	}
	if v.description == nil || *v.description != "Bring a, pen; paper" {
		t.Fatalf("description = %v", v.description)
	}
}

func TestParseICSUTCUsesFamilyZone(t *testing.T) {
	london := mustZone(t, "Europe/London")
	got, err := parseICS(wrapCalendar(vevent(
		"UID:utc@test",
		"DTSTART:20261012T080000Z",
		"DTEND:20261012T090000Z",
		"SUMMARY:Call",
	)), london)
	if err != nil || len(got.Series) != 1 {
		t.Fatalf("parse: %v, series %d", err, len(got.Series))
	}
	// 08:00 UTC is 09:00 in London during British Summer Time.
	v := got.Series[0].Event
	if v.zone.String() != "Europe/London" || v.timing.startTime != "09:00" || v.timing.endTime != "10:00" {
		t.Fatalf("zone %s timing %+v", v.zone, v.timing)
	}
}

func TestParseICSAllDayEndIsInclusive(t *testing.T) {
	s := parseOne(t, wrapCalendar(vevent(
		"UID:trip@test",
		"DTSTART;VALUE=DATE:20261012",
		"DTEND;VALUE=DATE:20261015",
		"SUMMARY:Trip",
	)))
	v := s.Event
	if !v.allDay || v.timing.startDate != "2026-10-12" || v.timing.endDate != "2026-10-14" {
		t.Fatalf("all-day = %v, timing %+v", v.allDay, v.timing)
	}
}

func TestParseICSAllDayDuration(t *testing.T) {
	s := parseOne(t, wrapCalendar(vevent(
		"UID:duration@test",
		"DTSTART;VALUE=DATE:20261012",
		"DURATION:P2D",
		"SUMMARY:Camp",
	)))
	if s.Event.timing.startDate != "2026-10-12" || s.Event.timing.endDate != "2026-10-13" {
		t.Fatalf("timing = %+v", s.Event.timing)
	}
}

func TestParseICSEndFallbacks(t *testing.T) {
	tests := []struct {
		name    string
		lines   []string
		endTime string
		endDate string
		skipped int
	}{
		{name: "duration", lines: []string{"DTSTART:20261012T090000Z", "DURATION:PT90M"}, endTime: "10:30", endDate: "2026-10-12"},
		{name: "no end uses an hour", lines: []string{"DTSTART:20261012T090000Z"}, endTime: "10:00", endDate: "2026-10-12"},
		{name: "end before start uses an hour", lines: []string{"DTSTART:20261012T090000Z", "DTEND:20261012T080000Z"}, endTime: "10:00", endDate: "2026-10-12"},
		{name: "unknown zone is skipped", lines: []string{"DTSTART;TZID=Mars Standard Time:20261012T090000"}, skipped: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lines := append([]string{"UID:end@test", "SUMMARY:Thing"}, tc.lines...)
			got, err := parseICS(wrapCalendar(vevent(lines...)), mustZone(t, "UTC"))
			if err != nil {
				t.Fatalf("parseICS: %v", err)
			}
			if got.Skipped != tc.skipped {
				t.Fatalf("skipped = %d, want %d", got.Skipped, tc.skipped)
			}
			if tc.skipped > 0 {
				if len(got.Series) != 0 {
					t.Fatalf("series = %d, want 0", len(got.Series))
				}
				return
			}
			v := got.Series[0].Event
			if v.timing.endTime != tc.endTime || v.timing.endDate != tc.endDate {
				t.Fatalf("end = %s %s, want %s %s", v.timing.endDate, v.timing.endTime, tc.endDate, tc.endTime)
			}
		})
	}
}

func TestParseICSUntitledAndLongTitle(t *testing.T) {
	s := parseOne(t, wrapCalendar(vevent("UID:blank@test", "DTSTART:20261012T090000Z")))
	if s.Event.title != untitledEvent {
		t.Fatalf("title = %q", s.Event.title)
	}

	long := strings.Repeat("é", maxTitleLen+30)
	s = parseOne(t, wrapCalendar(vevent("UID:long@test", "DTSTART:20261012T090000Z", "SUMMARY:"+long)))
	if got := len([]rune(s.Event.title)); got != maxTitleLen {
		t.Fatalf("title runes = %d, want %d", got, maxTitleLen)
	}
}

func TestParseICSFoldedLinesAndBOM(t *testing.T) {
	// A folded line continues with a leading space, and the file starts with a byte order mark.
	raw := append([]byte("\xEF\xBB\xBF"), wrapCalendar(vevent(
		"UID:fold@test",
		"DTSTART:20261012T090000Z",
		"SUMMARY:Family din",
		" ner",
	))...)
	s := parseOne(t, raw)
	if s.Event.title != "Family dinner" {
		t.Fatalf("title = %q", s.Event.title)
	}
}

func TestParseICSRecurrenceWithExdate(t *testing.T) {
	s := parseOne(t, wrapCalendar(vevent(
		"UID:swim@test",
		"DTSTART;TZID=Europe/London:20261012T090000",
		"DTEND;TZID=Europe/London:20261012T100000",
		"RRULE:FREQ=WEEKLY;BYDAY=MO",
		"EXDATE;TZID=Europe/London:20261019T090000,20261026T090000",
		"SUMMARY:Swimming",
	)))
	if s.Event.rule == nil || *s.Event.rule != "FREQ=WEEKLY;BYDAY=MO" {
		t.Fatalf("rule = %v", s.Event.rule)
	}
	if strings.Join(s.Cancelled, ",") != "2026-10-19,2026-10-26" {
		t.Fatalf("cancelled = %v", s.Cancelled)
	}
}

func TestParseICSUnsupportedRulesAreSkipped(t *testing.T) {
	tests := map[string]string{
		"hourly":           "RRULE:FREQ=HOURLY",
		"count with until": "RRULE:FREQ=WEEKLY;COUNT=3;UNTIL=20261231T000000Z",
		"by time of day":   "RRULE:FREQ=DAILY;BYHOUR=9",
	}
	for name, rule := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseICS(wrapCalendar(vevent(
				"UID:rule@test", "DTSTART:20261012T090000Z", "DTEND:20261012T100000Z", rule, "SUMMARY:Rule",
			)), mustZone(t, "UTC"))
			if err != nil {
				t.Fatalf("parseICS: %v", err)
			}
			if len(got.Series) != 0 || got.Skipped != 1 {
				t.Fatalf("series %d skipped %d, want 0 and 1", len(got.Series), got.Skipped)
			}
		})
	}
}

func TestParseICSCancelledRootIsSkipped(t *testing.T) {
	got, err := parseICS(wrapCalendar(vevent(
		"UID:gone@test", "DTSTART:20261012T090000Z", "STATUS:CANCELLED", "SUMMARY:Gone",
	)), mustZone(t, "UTC"))
	if err != nil {
		t.Fatalf("parseICS: %v", err)
	}
	if len(got.Series) != 0 || got.Skipped != 1 {
		t.Fatalf("series %d skipped %d", len(got.Series), got.Skipped)
	}
}

func TestParseICSOverrides(t *testing.T) {
	series := vevent(
		"UID:class@test",
		"DTSTART;TZID=Europe/London:20261012T090000",
		"DTEND;TZID=Europe/London:20261012T100000",
		"RRULE:FREQ=WEEKLY;BYDAY=MO",
		"SUMMARY:Class",
	)
	moved := vevent(
		"UID:class@test",
		"RECURRENCE-ID;TZID=Europe/London:20261019T090000",
		"DTSTART;TZID=Europe/London:20261020T140000",
		"DTEND;TZID=Europe/London:20261020T150000",
		"SUMMARY:Class (moved)",
	)
	cancelled := vevent(
		"UID:class@test",
		"RECURRENCE-ID;TZID=Europe/London:20261026T090000",
		"STATUS:CANCELLED",
	)
	t.Run("moved and cancelled", func(t *testing.T) {
		got, err := parseICS(wrapCalendar(series, moved, cancelled), mustZone(t, "UTC"))
		if err != nil {
			t.Fatalf("parseICS: %v", err)
		}
		if got.Skipped != 0 || len(got.Series) != 1 {
			t.Fatalf("series %d skipped %d", len(got.Series), got.Skipped)
		}
		s := got.Series[0]
		if len(s.Edited) != 1 || s.Edited[0].Date != "2026-10-19" {
			t.Fatalf("edited = %+v", s.Edited)
		}
		occ := s.Edited[0].Occurrence
		if occ.title != "Class (moved)" || occ.timing.startDate != "2026-10-20" || occ.timing.startTime != "14:00" {
			t.Fatalf("occurrence = %+v %+v", occ.title, occ.timing)
		}
		if strings.Join(s.Cancelled, ",") != "2026-10-26" {
			t.Fatalf("cancelled = %v", s.Cancelled)
		}
	})
	t.Run("override without a series becomes a one-off event", func(t *testing.T) {
		got, err := parseICS(wrapCalendar(moved), mustZone(t, "UTC"))
		if err != nil {
			t.Fatalf("parseICS: %v", err)
		}
		if len(got.Series) != 1 || got.Skipped != 0 {
			t.Fatalf("series %d skipped %d", len(got.Series), got.Skipped)
		}
		if got.Series[0].Event.title != "Class (moved)" || got.Series[0].Event.timing.startDate != "2026-10-20" {
			t.Fatalf("event = %+v %+v", got.Series[0].Event.title, got.Series[0].Event.timing)
		}
	})
	t.Run("override of a series without a rule is skipped", func(t *testing.T) {
		once := vevent("UID:once@test", "DTSTART:20261012T090000Z", "SUMMARY:Once")
		override := vevent(
			"UID:once@test",
			"RECURRENCE-ID:20261012T090000Z",
			"DTSTART:20261012T140000Z",
			"SUMMARY:Once (moved)",
		)
		got, err := parseICS(wrapCalendar(once, override), mustZone(t, "UTC"))
		if err != nil {
			t.Fatalf("parseICS: %v", err)
		}
		if len(got.Series) != 1 || got.Skipped != 1 || len(got.Series[0].Edited) != 0 {
			t.Fatalf("series %d skipped %d", len(got.Series), got.Skipped)
		}
	})
}

func TestParseICSDuplicateUIDsKeepTheNewestVersion(t *testing.T) {
	oldCopy := vevent(
		"UID:club@test", "DTSTART:20261012T090000Z", "DTEND:20261012T100000Z",
		"SUMMARY:Club (old)", "SEQUENCE:0", "DTSTAMP:20261001T000000Z",
	)
	newCopy := vevent(
		"UID:club@test", "DTSTART:20261012T090000Z", "DTEND:20261012T100000Z",
		"SUMMARY:Club (new)", "SEQUENCE:2", "DTSTAMP:20261005T000000Z",
	)
	laterStamp := vevent(
		"UID:club@test", "DTSTART:20261012T090000Z", "DTEND:20261012T100000Z",
		"SUMMARY:Club (later stamp)", "SEQUENCE:1", "DTSTAMP:20261006T000000Z",
	)
	earlierStamp := vevent(
		"UID:club@test", "DTSTART:20261012T090000Z", "DTEND:20261012T100000Z",
		"SUMMARY:Club (earlier stamp)", "SEQUENCE:1", "DTSTAMP:20261002T000000Z",
	)
	tieFirst := vevent(
		"UID:club@test", "DTSTART:20261012T090000Z", "DTEND:20261012T100000Z",
		"SUMMARY:Club (first)", "SEQUENCE:1", "DTSTAMP:20261003T000000Z",
	)
	tieSecond := vevent(
		"UID:club@test", "DTSTART:20261012T090000Z", "DTEND:20261012T100000Z",
		"SUMMARY:Club (second)", "SEQUENCE:1", "DTSTAMP:20261003T000000Z",
	)
	cases := []struct {
		name       string
		events     []string
		title      string
		duplicates int
	}{
		{"one copy is not a duplicate", []string{oldCopy}, "Club (old)", 0},
		{"higher SEQUENCE wins even when it comes first", []string{newCopy, oldCopy}, "Club (new)", 1},
		{"higher SEQUENCE wins over a later copy", []string{oldCopy, newCopy}, "Club (new)", 1},
		{"later DTSTAMP wins when SEQUENCE is equal", []string{earlierStamp, laterStamp}, "Club (later stamp)", 1},
		{"later copy wins when SEQUENCE and DTSTAMP are equal", []string{tieFirst, tieSecond}, "Club (second)", 1},
		{"three copies count two duplicates", []string{oldCopy, newCopy, laterStamp}, "Club (new)", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseICS(wrapCalendar(tc.events...), mustZone(t, "UTC"))
			if err != nil {
				t.Fatalf("parseICS: %v", err)
			}
			if len(got.Series) != 1 || got.Skipped != 0 || got.Duplicates != tc.duplicates {
				t.Fatalf("series %d skipped %d duplicates %d", len(got.Series), got.Skipped, got.Duplicates)
			}
			if got.Series[0].Event.title != tc.title {
				t.Fatalf("title = %q, want %q", got.Series[0].Event.title, tc.title)
			}
		})
	}
}

func TestParseICSOverridesApplyToTheKeptCopy(t *testing.T) {
	oldCopy := vevent(
		"UID:class@test", "DTSTART;TZID=Europe/London:20261012T090000", "DTEND;TZID=Europe/London:20261012T100000",
		"RRULE:FREQ=WEEKLY;BYDAY=MO", "SUMMARY:Class (old)", "SEQUENCE:0",
	)
	newCopy := vevent(
		"UID:class@test", "DTSTART;TZID=Europe/London:20261012T090000", "DTEND;TZID=Europe/London:20261012T100000",
		"RRULE:FREQ=WEEKLY;BYDAY=MO", "SUMMARY:Class (new)", "SEQUENCE:1",
	)
	moved := vevent(
		"UID:class@test",
		"RECURRENCE-ID;TZID=Europe/London:20261019T090000",
		"DTSTART;TZID=Europe/London:20261020T140000",
		"DTEND;TZID=Europe/London:20261020T150000",
		"SUMMARY:Class (moved)",
	)
	got, err := parseICS(wrapCalendar(oldCopy, newCopy, moved), mustZone(t, "UTC"))
	if err != nil {
		t.Fatalf("parseICS: %v", err)
	}
	if len(got.Series) != 1 || got.Skipped != 0 || got.Duplicates != 1 {
		t.Fatalf("series %d skipped %d duplicates %d", len(got.Series), got.Skipped, got.Duplicates)
	}
	s := got.Series[0]
	if s.Event.title != "Class (new)" || len(s.Edited) != 1 || s.Edited[0].Date != "2026-10-19" {
		t.Fatalf("series = %q edited %+v", s.Event.title, s.Edited)
	}
}

func TestParseICSOrphanOverrideDuplicatesAreMerged(t *testing.T) {
	first := vevent("UID:moved@test", "RECURRENCE-ID:20261019T090000Z", "DTSTART:20261020T140000Z", "DTEND:20261020T150000Z", "SUMMARY:Moved")
	second := vevent("UID:moved@test", "RECURRENCE-ID:20261019T090000Z", "DTSTART:20261020T140000Z", "DTEND:20261020T150000Z", "SUMMARY:Moved")
	got, err := parseICS(wrapCalendar(first, second), mustZone(t, "UTC"))
	if err != nil {
		t.Fatalf("parseICS: %v", err)
	}
	if len(got.Series) != 1 || got.Skipped != 0 || got.Duplicates != 1 {
		t.Fatalf("series %d skipped %d duplicates %d", len(got.Series), got.Skipped, got.Duplicates)
	}
}

func TestParseICSOverrideOfASkippedMasterIsSkipped(t *testing.T) {
	master := vevent("UID:hourly@test", "DTSTART:20261012T090000Z", "RRULE:FREQ=HOURLY", "SUMMARY:Hourly")
	override := vevent("UID:hourly@test", "RECURRENCE-ID:20261012T100000Z", "DTSTART:20261012T110000Z", "SUMMARY:Hourly (moved)")
	got, err := parseICS(wrapCalendar(master, override), mustZone(t, "UTC"))
	if err != nil {
		t.Fatalf("parseICS: %v", err)
	}
	if len(got.Series) != 0 || got.Skipped != 2 {
		t.Fatalf("series %d skipped %d, want 0 and 2", len(got.Series), got.Skipped)
	}
}

func TestParseICSCancelledOrphanOverrideIsSkipped(t *testing.T) {
	cancelled := vevent("UID:gone@test", "RECURRENCE-ID:20261019T090000Z", "STATUS:CANCELLED")
	got, err := parseICS(wrapCalendar(cancelled), mustZone(t, "UTC"))
	if err != nil {
		t.Fatalf("parseICS: %v", err)
	}
	if len(got.Series) != 0 || got.Skipped != 1 || got.Duplicates != 0 {
		t.Fatalf("series %d skipped %d duplicates %d", len(got.Series), got.Skipped, got.Duplicates)
	}
}

func TestParseICSZoneNamesFromOtherApps(t *testing.T) {
	cases := []struct {
		name    string
		tzid    string
		zone    string
		skipped int
	}{
		{"IANA name", "Europe/Paris", "Europe/Paris", 0},
		{"Outlook name", "Eastern Standard Time", "America/New_York", 0},
		{"Outlook GMT name", "GMT Standard Time", "Europe/London", 0},
		{"Thunderbird path", "/mozilla.org/20050126_1/Europe/Paris", "Europe/Paris", 0},
		{"unknown name is skipped", "Mars Standard Time", "", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := wrapCalendar(vevent(
				"UID:zone@test",
				"DTSTART;TZID="+tc.tzid+":20261012T090000",
				"DTEND;TZID="+tc.tzid+":20261012T100000",
				"SUMMARY:Zoned",
			))
			got, err := parseICS(data, mustZone(t, "UTC"))
			if err != nil {
				t.Fatalf("parseICS: %v", err)
			}
			if got.Skipped != tc.skipped {
				t.Fatalf("skipped = %d, want %d", got.Skipped, tc.skipped)
			}
			if tc.skipped > 0 {
				if len(got.Series) != 0 {
					t.Fatalf("series = %d, want 0 for an unknown zone", len(got.Series))
				}
				return
			}
			if len(got.Series) != 1 {
				t.Fatalf("series = %d, want 1", len(got.Series))
			}
			v := got.Series[0].Event
			if v.zone.String() != tc.zone {
				t.Fatalf("zone = %s, want %s", v.zone, tc.zone)
			}
			if v.timing.startTime != "09:00" || v.timing.startDate != "2026-10-12" {
				t.Fatalf("start = %s %s, want 2026-10-12 09:00 in %s", v.timing.startDate, v.timing.startTime, tc.zone)
			}
		})
	}
}

func TestParseICSRejectsInvalidFiles(t *testing.T) {
	if _, err := parseICS([]byte("not a calendar"), mustZone(t, "UTC")); !errors.Is(err, errInvalidICS) {
		t.Fatalf("garbage err = %v, want errInvalidICS", err)
	}

	var events []string
	for i := 0; i <= maxImportEvents; i++ {
		events = append(events, vevent(
			fmt.Sprintf("UID:e%d@test", i), "DTSTART:20261012T090000Z", "SUMMARY:Event",
		))
	}
	if _, err := parseICS(wrapCalendar(events...), mustZone(t, "UTC")); !errors.Is(err, errTooManyEvents) {
		t.Fatalf("too many err = %v, want errTooManyEvents", err)
	}
}
