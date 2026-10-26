// Package calendar implements the family's local calendar: events, their repeats and the changes
// to single occurrences (PBI M5-01). Events are stored once and expanded on read, in the zone the
// event was made in, so a repeating event keeps its local time across daylight saving changes.
package calendar

import (
	"sort"
	"time"

	"nestwall/backend/internal/tasks"
)

const dateLayout = "2006-01-02"

// Occurrence is one showing of an event between two instants.
type Occurrence struct {
	EventID        string
	OccurrenceDate string // local date of the occurrence in the series, YYYY-MM-DD
	Title          string
	Description    *string
	Location       *string
	AllDay         bool
	TimeZone       string
	StartsAt       time.Time
	EndsAt         time.Time
	Recurring      bool
	Edited         bool
}

// Series is a stored event in the form Expand reads.
type Series struct {
	ID              string
	Title           string
	Description     *string
	Location        *string
	AllDay          bool
	Zone            *time.Location
	StartDate       string // local date of the first occurrence, YYYY-MM-DD
	StartHour       int    // timed events only
	StartMinute     int    // timed events only
	SpanDays        int    // all-day events only: days after StartDate that the event runs
	DurationMinutes int    // timed events only
	Rule            string // canonical RRULE; empty when the event does not repeat
}

// Exception is a stored change to one occurrence of a repeating event. A cancelled occurrence has
// no other fields. An edited one carries its new title and UTC instants.
type Exception struct {
	OccurrenceDate string
	Cancelled      bool
	Title          string
	Description    *string
	Location       *string
	StartsAt       time.Time
	EndsAt         time.Time
}

// Expand returns the occurrences of s that overlap the half-open window [from, to), sorted by start.
// Cancelled occurrences are dropped. An edited occurrence is returned in its new place, even when
// it has moved in from a date outside the window. An exception whose original date is not an
// occurrence of the series is ignored.
func Expand(s Series, exceptions []Exception, from, to time.Time) ([]Occurrence, error) {
	if !to.After(from) {
		return nil, nil
	}
	startDay, err := time.Parse(dateLayout, s.StartDate)
	if err != nil {
		return nil, err
	}
	if s.Rule == "" {
		start, end := s.base(startDay)
		if !overlaps(start, end, from, to) {
			return nil, nil
		}
		return []Occurrence{s.occurrence(s.StartDate, s.Title, s.Description, s.Location, start, end, false)}, nil
	}

	lo := localDay(from, s.Zone).AddDate(0, 0, -s.lookbackDays())
	hi := localDay(to, s.Zone).AddDate(0, 0, 1)
	dates, err := tasks.OccurrenceDates(s.Rule, startDay, lo, hi)
	if err != nil {
		return nil, err
	}

	cancelled := map[string]bool{}
	edits := map[string]Exception{}
	for _, e := range exceptions {
		if e.Cancelled {
			cancelled[e.OccurrenceDate] = true
		} else {
			edits[e.OccurrenceDate] = e
		}
	}

	var out []Occurrence
	inWindow := map[string]bool{}
	for _, d := range dates {
		key := d.Format(dateLayout)
		inWindow[key] = true
		if cancelled[key] {
			continue
		}
		var o Occurrence
		if e, ok := edits[key]; ok {
			o = s.fromException(e)
		} else {
			start, end := s.base(d)
			o = s.occurrence(key, s.Title, s.Description, s.Location, start, end, false)
		}
		if overlaps(o.StartsAt, o.EndsAt, from, to) {
			out = append(out, o)
		}
	}

	// An edit can move an occurrence into the window even though its original date lies outside.
	for key, e := range edits {
		if inWindow[key] {
			continue
		}
		day, err := time.Parse(dateLayout, key)
		if err != nil {
			continue
		}
		valid, err := tasks.OccurrenceDates(s.Rule, startDay, day, day)
		if err != nil {
			return nil, err
		}
		if len(valid) == 0 || cancelled[key] {
			continue
		}
		if o := s.fromException(e); overlaps(o.StartsAt, o.EndsAt, from, to) {
			out = append(out, o)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].StartsAt.Equal(out[j].StartsAt) {
			return out[i].StartsAt.Before(out[j].StartsAt)
		}
		return out[i].OccurrenceDate < out[j].OccurrenceDate
	})
	return out, nil
}

// base returns the instants of the occurrence that starts on the local date day (UTC midnight).
// A timed occurrence keeps its local start time, so its UTC instant follows the zone's offset.
func (s Series) base(day time.Time) (start, end time.Time) {
	if s.AllDay {
		start = localMidnight(day, s.Zone)
		return start, localMidnight(day.AddDate(0, 0, s.SpanDays+1), s.Zone)
	}
	start = time.Date(day.Year(), day.Month(), day.Day(), s.StartHour, s.StartMinute, 0, 0, s.Zone).UTC()
	return start, start.Add(time.Duration(s.DurationMinutes) * time.Minute)
}

// fromException returns the occurrence an edited exception replaces.
func (s Series) fromException(e Exception) Occurrence {
	return s.occurrence(e.OccurrenceDate, e.Title, e.Description, e.Location, e.StartsAt, e.EndsAt, true)
}

func (s Series) occurrence(key, title string, desc, loc *string, start, end time.Time, edited bool) Occurrence {
	return Occurrence{
		EventID:        s.ID,
		OccurrenceDate: key,
		Title:          title,
		Description:    desc,
		Location:       loc,
		AllDay:         s.AllDay,
		TimeZone:       s.Zone.String(),
		StartsAt:       start,
		EndsAt:         end,
		Recurring:      s.Rule != "",
		Edited:         edited,
	}
}

// lookbackDays is how many days before the window an occurrence can start and still overlap it.
func (s Series) lookbackDays() int {
	if s.AllDay {
		return s.SpanDays + 2
	}
	return s.DurationMinutes/(24*60) + 2
}

// overlaps reports whether [start, end) meets [from, to).
func overlaps(start, end, from, to time.Time) bool {
	return start.Before(to) && end.After(from)
}

// localDay returns the local date of t in zone, as UTC midnight.
func localDay(t time.Time, zone *time.Location) time.Time {
	l := t.In(zone)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// localMidnight returns local midnight of the civil date day (UTC midnight) in zone, as UTC.
func localMidnight(day time.Time, zone *time.Location) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, zone).UTC()
}
