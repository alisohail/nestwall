package calendar

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	ical "github.com/emersion/go-ical"
)

const (
	// maxImportEvents bounds one file. A larger calendar should be exported in parts.
	maxImportEvents = 5000
	// importDefaultMinutes is the length given to a timed event that has no end.
	importDefaultMinutes = 60
	untitledEvent        = "Untitled event"
)

// icsParserVersion is stored with each linked calendar. Bump it when parseICS changes what it
// stores, so linked calendars parsed by an older version are fetched again even if the source
// reports no change.
const icsParserVersion = 1

var (
	errInvalidICS    = errors.New("invalid ics")
	errTooManyEvents = errors.New("too many events")
)

// parsedImport is what one ICS file turns into. Each series is one event in NestWall. Skipped
// counts the VEVENTs that could not be imported, such as an unknown time zone or a repeat rule
// NestWall does not support. Duplicates counts the VEVENTs merged into another with the same UID.
type parsedImport struct {
	Series     []importedSeries
	Skipped    int
	Duplicates int
}

// importedSeries is one VEVENT that is stored as an event. Cancelled holds the dates of occurrences
// that were removed (EXDATE or a cancelled override); Edited holds the occurrences that were moved
// or retitled.
type importedSeries struct {
	UID       string
	Event     validatedEvent
	Cancelled []string
	Edited    []editedOccurrence
}

// editedOccurrence is one occurrence of a series that the file changed. Date is its original
// local date in the series' zone.
type editedOccurrence struct {
	Date       string
	Occurrence validatedOccurrence
}

// parseICS reads an ICS file and maps each VEVENT to an event. familyZone is used for times that
// are UTC or have no zone, and for all-day events.
func parseICS(data []byte, familyZone *time.Location) (parsedImport, error) {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil || cal == nil || cal.Component == nil {
		return parsedImport{}, errInvalidICS
	}

	var parents, overrides []*ical.Component
	masters := map[string]bool{}
	for _, child := range cal.Children {
		if child.Name != "VEVENT" {
			continue
		}
		if len(parents)+len(overrides) == maxImportEvents {
			return parsedImport{}, errTooManyEvents
		}
		resolveTZIDs(child)
		if child.Props.Get(ical.PropRecurrenceID) != nil {
			overrides = append(overrides, child)
		} else {
			parents = append(parents, child)
			masters[textOf(child, "UID")] = true
		}
	}

	// An override whose UID has no master in the file is a single event that was moved, so it is
	// imported like any other one-off event.
	var attach []*ical.Component
	for _, c := range overrides {
		if masters[textOf(c, "UID")] {
			attach = append(attach, c)
		} else {
			parents = append(parents, c)
		}
	}

	var out parsedImport
	found := make([]candidate, 0, len(parents))
	for _, c := range parents {
		series, err := parseSeries(c, familyZone)
		if err != nil {
			out.Skipped++
			continue
		}
		found = append(found, candidate{series: series, sequence: sequenceOf(c), stamped: stampOf(c), position: len(found)})
	}

	// Several VEVENTs with one UID are one event that was exported more than once. The newest
	// version wins: higher SEQUENCE, then later DTSTAMP, then the later one in the file.
	best := map[string]candidate{}
	for _, c := range found {
		prev, seen := best[c.series.UID]
		if !seen {
			best[c.series.UID] = c
			continue
		}
		out.Duplicates++
		if c.supersedes(prev) {
			best[c.series.UID] = c
		}
	}
	for _, c := range found {
		if best[c.series.UID].position == c.position {
			out.Series = append(out.Series, c.series)
		}
	}

	byUID := make(map[string]int, len(out.Series))
	for i := range out.Series {
		byUID[out.Series[i].UID] = i
	}
	for _, c := range attach {
		idx, ok := byUID[textOf(c, "UID")]
		if !ok || out.Series[idx].Event.rule == nil {
			out.Skipped++
			continue
		}
		if err := attachOverride(&out.Series[idx], c); err != nil {
			out.Skipped++
		}
	}
	return out, nil
}

// candidate is a VEVENTs that parsed, with what decides between duplicates of its UID.
type candidate struct {
	series   importedSeries
	sequence int
	stamped  time.Time
	position int
}

func (c candidate) supersedes(prev candidate) bool {
	if c.sequence != prev.sequence {
		return c.sequence > prev.sequence
	}
	return !c.stamped.Before(prev.stamped)
}

// sequenceOf reads SEQUENCE, which counts the revisions of an event. A missing or bad value is 0.
func sequenceOf(c *ical.Component) int {
	prop := c.Props.Get(ical.PropSequence)
	if prop == nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(prop.Value))
	if err != nil {
		return 0
	}
	return n
}

// stampOf reads DTSTAMP, when the file was made. A missing value is the zero time.
func stampOf(c *ical.Component) time.Time {
	t, err := c.Props.DateTime(ical.PropDateTimeStamp, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseSeries maps a VEVENT that is not an override to a validated event.
func parseSeries(c *ical.Component, familyZone *time.Location) (importedSeries, error) {
	if strings.EqualFold(textOf(c, "STATUS"), "CANCELLED") {
		return importedSeries{}, fieldError("status", "cancelled")
	}
	startProp := c.Props.Get(ical.PropDateTimeStart)
	if startProp == nil {
		return importedSeries{}, fieldError("DTSTART", "required")
	}
	allDay := isDateOnly(startProp)
	zone, err := eventZone(startProp, familyZone)
	if err != nil {
		return importedSeries{}, err
	}
	start, err := instant(startProp, zone)
	if err != nil {
		return importedSeries{}, fieldError("DTSTART", "invalid")
	}
	end, err := itemEnd(c.Props, start, allDay, zone)
	if err != nil {
		return importedSeries{}, err
	}

	req := eventRequest{
		Title:    clip(textOf(c, "SUMMARY"), maxTitleLen),
		AllDay:   allDay,
		TimeZone: zone.String(),
	}
	if req.Title == "" {
		req.Title = untitledEvent
	}
	req.Description = optionalClip(textOf(c, "DESCRIPTION"), maxDescriptionLen)
	req.Location = optionalClip(textOf(c, "LOCATION"), maxLocationLen)
	if rule := c.Props.Get(ical.PropRecurrenceRule); rule != nil {
		value := rule.Value
		req.RecurrenceRule = &value
	}
	f := localFields(start, end, zone, allDay)
	req.StartDate, req.StartTime = f.startDate, optionalString(f.startTime)
	req.EndDate, req.EndTime = optionalString(f.endDate), optionalString(f.endTime)

	v, err := req.check()
	if err != nil {
		return importedSeries{}, err
	}
	v.reminders = alarmMinutes(c)
	series := importedSeries{UID: textOf(c, "UID"), Event: v}
	if req.RecurrenceRule != nil {
		series.Cancelled = exdates(c.Props, zone)
	}
	return series, nil
}

// attachOverride adds a RECURRENCE-ID override to its series. An override with STATUS:CANCELLED
// removes that occurrence; any other override moves or retitles it.
func attachOverride(series *importedSeries, c *ical.Component) error {
	zone := series.Event.zone
	allDay := series.Event.allDay

	recID := c.Props.Get(ical.PropRecurrenceID)
	recAt, err := instant(recID, zone)
	if err != nil {
		return fieldError("RECURRENCE-ID", "invalid")
	}
	date := recAt.In(zone).Format(dateLayout)

	if strings.EqualFold(textOf(c, "STATUS"), "CANCELLED") {
		series.Cancelled = append(series.Cancelled, date)
		return nil
	}

	startProp := c.Props.Get(ical.PropDateTimeStart)
	if startProp == nil || isDateOnly(startProp) != allDay {
		return fieldError("DTSTART", "invalid")
	}
	start, err := instant(startProp, zone)
	if err != nil {
		return fieldError("DTSTART", "invalid")
	}
	end, err := itemEnd(c.Props, start, allDay, zone)
	if err != nil {
		return err
	}

	title := clip(textOf(c, "SUMMARY"), maxTitleLen)
	if title == "" {
		title = series.Event.title
	}
	req := occurrenceRequest{
		Title:       title,
		Description: optionalClip(textOf(c, "DESCRIPTION"), maxDescriptionLen),
		Location:    optionalClip(textOf(c, "LOCATION"), maxLocationLen),
	}
	f := localFields(start, end, zone, allDay)
	req.StartDate, req.StartTime = f.startDate, optionalString(f.startTime)
	req.EndDate, req.EndTime = optionalString(f.endDate), optionalString(f.endTime)

	occ, err := req.check(allDay, zone)
	if err != nil {
		return err
	}
	series.Edited = append(series.Edited, editedOccurrence{Date: date, Occurrence: occ})
	return nil
}

// resolveTZIDs rewrites each TZID in a VEVENT to the IANA name icsZone reads it as. The ICS library
// looks zones up by IANA name only, so a Windows or path name must be rewritten before it reads a
// time. A name that does not resolve is left as written, so its event fails to parse and is skipped.
func resolveTZIDs(c *ical.Component) {
	for _, props := range c.Props {
		for i := range props {
			tzid := props[i].Params.Get(ical.PropTimezoneID)
			if tzid == "" {
				continue
			}
			if loc, err := icsZone(tzid); err == nil {
				props[i].Params.Set(ical.PropTimezoneID, loc.String())
			}
		}
	}
}

// eventZone returns the zone a DTSTART is written in. A TZID names it. UTC, floating and all-day
// values use the family zone.
func eventZone(prop *ical.Prop, familyZone *time.Location) (*time.Location, error) {
	tzid := prop.Params.Get(ical.PropTimezoneID)
	if isDateOnly(prop) || tzid == "" || strings.HasSuffix(prop.Value, "Z") {
		return familyZone, nil
	}
	return icsZone(tzid)
}

// windowsZones maps the zone names Outlook and other Windows software write in a TZID to IANA
// names. It covers common zones only; an unlisted name is skipped, not guessed.
var windowsZones = map[string]string{
	"Dateline Standard Time":          "Etc/GMT+12",
	"UTC-11":                          "Etc/GMT+11",
	"Hawaiian Standard Time":          "Pacific/Honolulu",
	"Alaskan Standard Time":           "America/Anchorage",
	"Pacific Standard Time":           "America/Los_Angeles",
	"Mountain Standard Time":          "America/Denver",
	"US Mountain Standard Time":       "America/Phoenix",
	"Central Standard Time":           "America/Chicago",
	"Canada Central Standard Time":    "America/Regina",
	"Eastern Standard Time":           "America/New_York",
	"US Eastern Standard Time":        "America/Indianapolis",
	"Atlantic Standard Time":          "America/Halifax",
	"SA Pacific Standard Time":        "America/Bogota",
	"E. South America Standard Time":  "America/Sao_Paulo",
	"Argentina Standard Time":         "America/Buenos_Aires",
	"Azores Standard Time":            "Atlantic/Azores",
	"GMT Standard Time":               "Europe/London",
	"Greenwich Standard Time":         "Atlantic/Reykjavik",
	"W. Europe Standard Time":         "Europe/Berlin",
	"Central Europe Standard Time":    "Europe/Budapest",
	"Central European Standard Time":  "Europe/Warsaw",
	"Romance Standard Time":           "Europe/Paris",
	"W. Central Africa Standard Time": "Africa/Lagos",
	"GTB Standard Time":               "Europe/Bucharest",
	"E. Europe Standard Time":         "Europe/Chisinau",
	"FLE Standard Time":               "Europe/Kiev",
	"Israel Standard Time":            "Asia/Jerusalem",
	"South Africa Standard Time":      "Africa/Johannesburg",
	"Russian Standard Time":           "Europe/Moscow",
	"Arabian Standard Time":           "Asia/Dubai",
	"Pakistan Standard Time":          "Asia/Karachi",
	"India Standard Time":             "Asia/Kolkata",
	"Sri Lanka Standard Time":         "Asia/Colombo",
	"SE Asia Standard Time":           "Asia/Bangkok",
	"China Standard Time":             "Asia/Shanghai",
	"Singapore Standard Time":         "Asia/Singapore",
	"Tokyo Standard Time":             "Asia/Tokyo",
	"Korea Standard Time":             "Asia/Seoul",
	"W. Australia Standard Time":      "Australia/Perth",
	"Cen. Australia Standard Time":    "Australia/Adelaide",
	"AUS Central Standard Time":       "Australia/Darwin",
	"E. Australia Standard Time":      "Australia/Brisbane",
	"AUS Eastern Standard Time":       "Australia/Sydney",
	"Tasmania Standard Time":          "Australia/Hobart",
	"New Zealand Standard Time":       "Pacific/Auckland",
	"UTC":                             "UTC",
}

// icsZone returns the zone a TZID in an imported calendar names. Calendar apps write zones in
// different ways, so it tries an IANA name, then a Windows name, then the tail of a path such as
// Thunderbird's "/mozilla.org/20050126_1/Europe/Paris". An unknown name is an error, so its events
// are skipped and counted rather than shifted to the wrong time.
func icsZone(tzid string) (*time.Location, error) {
	name := strings.TrimSpace(tzid)
	if iana, ok := windowsZones[name]; ok {
		name = iana
	}
	if loc, err := loadZone(name); err == nil {
		return loc, nil
	}
	if strings.HasPrefix(name, "/") {
		parts := strings.Split(strings.Trim(name, "/"), "/")
		for i := range parts {
			if loc, err := loadZone(strings.Join(parts[i:], "/")); err == nil {
				return loc, nil
			}
		}
	}
	return nil, fieldError("TZID", "invalid")
}

// instant reads a DATE or DATE-TIME property and returns it in zone. A date is midnight in zone.
func instant(prop *ical.Prop, zone *time.Location) (time.Time, error) {
	t, err := prop.DateTime(zone)
	if err != nil {
		return time.Time{}, err
	}
	return t.In(zone), nil
}

// itemEnd returns the instant an item ends. It uses DTEND, then DURATION, then a default length.
// For an all-day item the end is the first day after it, as iCalendar writes it.
func itemEnd(props ical.Props, start time.Time, allDay bool, zone *time.Location) (time.Time, error) {
	if p := props.Get(ical.PropDateTimeEnd); p != nil {
		if isDateOnly(p) != allDay {
			return time.Time{}, fieldError("DTEND", "invalid")
		}
		end, err := instant(p, zone)
		if err != nil {
			return time.Time{}, fieldError("DTEND", "invalid")
		}
		if allDay || end.After(start) {
			return end, nil
		}
		return start.Add(importDefaultMinutes * time.Minute), nil
	}
	if p := props.Get(ical.PropDuration); p != nil {
		d, err := p.Duration()
		if err != nil || d <= 0 {
			return time.Time{}, fieldError("DURATION", "invalid")
		}
		if allDay {
			return start.AddDate(0, 0, int(d/(24*time.Hour))), nil
		}
		return start.Add(d), nil
	}
	if allDay {
		return start.AddDate(0, 0, 1), nil
	}
	return start.Add(importDefaultMinutes * time.Minute), nil
}

// exdates returns the local dates, in zone, of the occurrences a series excludes.
func exdates(props ical.Props, zone *time.Location) []string {
	var out []string
	for _, p := range props.Values(ical.PropExceptionDates) {
		for _, value := range strings.Split(p.Value, ",") {
			single := ical.Prop{Name: p.Name, Params: p.Params, Value: strings.TrimSpace(value)}
			t, err := instant(&single, zone)
			if err != nil {
				continue
			}
			out = append(out, t.In(zone).Format(dateLayout))
		}
	}
	return out
}

// localWindow is an item's local start and end, as the calendar stores them.
type localWindow struct {
	startDate, startTime, endDate, endTime string
}

// localFields writes an item's start and end in zone. An all-day end is the last day it covers,
// so an end that falls on the start date stays on that date.
func localFields(start, end time.Time, zone *time.Location, allDay bool) localWindow {
	if allDay {
		s := civilDate(start)
		e := civilDate(end).AddDate(0, 0, -1)
		if e.Before(s) {
			e = s
		}
		return localWindow{startDate: s.Format(dateLayout), endDate: e.Format(dateLayout)}
	}
	s, e := start.In(zone), end.In(zone)
	return localWindow{
		startDate: s.Format(dateLayout),
		startTime: s.Format("15:04"),
		endDate:   e.Format(dateLayout),
		endTime:   e.Format("15:04"),
	}
}

// civilDate returns the calendar date of t as midnight UTC, so date arithmetic ignores time zones.
func civilDate(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// isDateOnly reports whether a DTSTART, DTEND or similar property holds a DATE, not a DATE-TIME.
func isDateOnly(p *ical.Prop) bool {
	return p.ValueType() == ical.ValueDate || len(p.Value) == 8
}

// textOf returns a TEXT property, unescaped, or an empty string.
func textOf(c *ical.Component, name string) string {
	p := c.Props.Get(name)
	if p == nil {
		return ""
	}
	// Many feeds leave commas unescaped (e.g. "Toronto, ON"). Prop.Text keeps only
	// the first list item, so rejoin the items to keep the whole value.
	items, err := p.TextList()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.Join(items, ","))
}

// clip shortens s to at most max characters, keeping whole characters.
func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

// optionalClip returns s clipped to max characters, or nil when it is empty.
func optionalClip(s string, max int) *string {
	s = clip(s, max)
	if s == "" {
		return nil
	}
	return &s
}

// optionalString returns nil for an empty string, so the field is left out.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
