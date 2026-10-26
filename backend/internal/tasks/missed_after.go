package tasks

import (
	"time"
)

// Units of the family missed-after cut-off. Months are calendar months, so they have no fixed length.
const (
	unitDays   = "days"
	unitWeeks  = "weeks"
	unitMonths = "months"
)

const maxMissedAfterValue = 999

// MissedAfter is the family cut-off. It applies to a task that sets no grace period of its own.
// A nil cut-off means the task never expires.
type MissedAfter struct {
	Value int    `json:"value"`
	Unit  string `json:"unit"`
}

// validate checks the count range and the unit.
func (m MissedAfter) validate() error {
	if m.Value < 1 || m.Value > maxMissedAfterValue {
		return &FieldError{Field: "missed_after", Reason: "invalid"}
	}
	switch m.Unit {
	case unitDays, unitWeeks, unitMonths:
		return nil
	}
	return &FieldError{Field: "missed_after", Reason: "invalid"}
}

// expiry returns the instant a task due at due expires under the cut-off. Counting is done in the
// family's zone, so a day or month keeps the local clock time.
func (m MissedAfter) expiry(due time.Time, zone *time.Location) time.Time {
	local := due.In(zone)
	switch m.Unit {
	case unitWeeks:
		local = local.AddDate(0, 0, 7*m.Value)
	case unitMonths:
		local = addMonths(local, m.Value)
	default:
		local = local.AddDate(0, 0, m.Value)
	}
	return local.UTC()
}

// addMonths adds n calendar months. A day past the end of the target month moves to its last day.
func addMonths(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	lastDay := time.Date(y, m+time.Month(n)+1, 0, 0, 0, 0, 0, t.Location()).Day()
	if d > lastDay {
		d = lastDay
	}
	return time.Date(y, m+time.Month(n), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// occurrenceExpiry returns when an occurrence expires, or nil when it never does. A grace period
// set on the task wins over the family cut-off.
func occurrenceExpiry(due time.Time, zone *time.Location, graceMinutes *int, cutoff *MissedAfter) *time.Time {
	var expires time.Time
	switch {
	case graceMinutes != nil:
		expires = due.Add(time.Duration(*graceMinutes) * time.Minute)
	case cutoff != nil:
		expires = cutoff.expiry(due, zone)
	default:
		return nil
	}
	return &expires
}
