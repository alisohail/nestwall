package calendar

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nestwall/backend/internal/tasks"
)

const (
	maxTitleLen        = 200
	maxDescriptionLen  = 2000
	maxLocationLen     = 200
	maxRuleLen         = 500
	maxMemberIDs       = 50
	maxSpanDays        = 3650
	maxDurationMinutes = 525600
)

var (
	datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	timePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
)

// FieldError is a request field that failed validation. Reason is a short code such as
// required or invalid.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string {
	return e.Field + ": " + e.Reason
}

func fieldError(field, reason string) error {
	return &FieldError{Field: field, Reason: reason}
}

// eventRequest is the body for creating an event and for replacing one (PUT).
type eventRequest struct {
	Title          string   `json:"title"`
	Description    *string  `json:"description"`
	Location       *string  `json:"location"`
	AllDay         bool     `json:"all_day"`
	TimeZone       string   `json:"time_zone"`
	StartDate      string   `json:"start_date"`
	StartTime      *string  `json:"start_time"`
	EndDate        *string  `json:"end_date"`
	EndTime        *string  `json:"end_time"`
	RecurrenceRule *string  `json:"recurrence_rule"`
	MemberIDs      []string `json:"member_ids"`
	CalendarID     *string  `json:"calendar_id"`
	Reminders      []int    `json:"reminders"`
}

// occurrenceRequest is the body for editing one occurrence of a repeating event. The timing
// follows the event: an all-day event takes dates only.
type occurrenceRequest struct {
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Location    *string `json:"location"`
	StartDate   string  `json:"start_date"`
	StartTime   *string `json:"start_time"`
	EndDate     *string `json:"end_date"`
	EndTime     *string `json:"end_time"`
}

// timing is when an event or one occurrence happens, in the forms the database stores.
type timing struct {
	startsAt        time.Time
	endsAt          time.Time
	startDate       string
	startTime       string
	endDate         string
	endTime         string
	durationMinutes int
	spanDays        int
}

// validatedEvent is an event request that has passed every check except the database ones.
type validatedEvent struct {
	title       string
	description *string
	location    *string
	allDay      bool
	zone        *time.Location
	timing      timing
	rule        *string
	memberIDs   []string
	reminders   []int
}

// validatedOccurrence is an occurrence edit that has passed validation.
type validatedOccurrence struct {
	title       string
	description *string
	location    *string
	timing      timing
}

func (req eventRequest) check() (validatedEvent, error) {
	var v validatedEvent
	var err error
	if v.title, err = requiredText("title", req.Title, maxTitleLen); err != nil {
		return v, err
	}
	if v.description, err = optionalText("description", req.Description, maxDescriptionLen); err != nil {
		return v, err
	}
	if v.location, err = optionalText("location", req.Location, maxLocationLen); err != nil {
		return v, err
	}
	if v.zone, err = loadZone(req.TimeZone); err != nil {
		return v, err
	}
	v.allDay = req.AllDay
	if v.timing, err = resolveTiming(req.AllDay, v.zone, req.StartDate,
		derefString(req.StartTime), derefString(req.EndDate), derefString(req.EndTime)); err != nil {
		return v, err
	}
	if v.rule, err = canonicalRule(req.RecurrenceRule); err != nil {
		return v, err
	}
	if v.memberIDs, err = normaliseMembers(req.MemberIDs); err != nil {
		return v, err
	}
	if v.reminders, err = normaliseReminders(req.Reminders); err != nil {
		return v, err
	}
	return v, nil
}

func (req occurrenceRequest) check(allDay bool, zone *time.Location) (validatedOccurrence, error) {
	var v validatedOccurrence
	var err error
	if v.title, err = requiredText("title", req.Title, maxTitleLen); err != nil {
		return v, err
	}
	if v.description, err = optionalText("description", req.Description, maxDescriptionLen); err != nil {
		return v, err
	}
	if v.location, err = optionalText("location", req.Location, maxLocationLen); err != nil {
		return v, err
	}
	if v.timing, err = resolveTiming(allDay, zone, req.StartDate,
		derefString(req.StartTime), derefString(req.EndDate), derefString(req.EndTime)); err != nil {
		return v, err
	}
	return v, nil
}

// resolveTiming checks the start and end of an event or occurrence in zone and works out its
// UTC instants. A timed item needs a start and an end time, and the end must come after the
// start. An all-day item takes dates only; its end date is the last day it covers and defaults
// to the start date. The duration of a timed item is absolute, so it does not change across a
// daylight saving change.
func resolveTiming(allDay bool, zone *time.Location, startDate, startTime, endDate, endTime string) (timing, error) {
	start, err := parseDate("start_date", startDate)
	if err != nil {
		return timing{}, err
	}
	if endDate == "" {
		endDate = startDate
	}
	end, err := parseDate("end_date", endDate)
	if err != nil {
		return timing{}, err
	}
	if end.Before(start) {
		return timing{}, fieldError("end_date", "before_start")
	}

	t := timing{startDate: startDate, endDate: endDate}
	if allDay {
		if startTime != "" {
			return timing{}, fieldError("start_time", "not_allowed")
		}
		if endTime != "" {
			return timing{}, fieldError("end_time", "not_allowed")
		}
		t.spanDays = int(end.Sub(start) / (24 * time.Hour))
		if t.spanDays > maxSpanDays {
			return timing{}, fieldError("end_date", "too_long")
		}
		t.startsAt = localMidnight(start, zone)
		t.endsAt = localMidnight(end.AddDate(0, 0, 1), zone)
		return t, nil
	}

	sh, sm, err := parseClock("start_time", startTime)
	if err != nil {
		return timing{}, err
	}
	eh, em, err := parseClock("end_time", endTime)
	if err != nil {
		return timing{}, err
	}
	t.startTime, t.endTime = startTime, endTime
	t.startsAt = wallInstant(start, sh, sm, zone)
	t.endsAt = wallInstant(end, eh, em, zone)
	if !t.endsAt.After(t.startsAt) {
		return timing{}, fieldError("end_time", "before_start")
	}
	t.durationMinutes = int(t.endsAt.Sub(t.startsAt) / time.Minute)
	if t.durationMinutes > maxDurationMinutes {
		return timing{}, fieldError("end_time", "too_long")
	}
	return t, nil
}

// parseDate reads a YYYY-MM-DD date as UTC midnight.
func parseDate(field, s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fieldError(field, "required")
	}
	if !datePattern.MatchString(s) {
		return time.Time{}, fieldError(field, "invalid")
	}
	d, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}, fieldError(field, "invalid")
	}
	return d, nil
}

// parseClock reads an HH:MM local time.
func parseClock(field, s string) (hour, minute int, err error) {
	if s == "" {
		return 0, 0, fieldError(field, "required")
	}
	if !timePattern.MatchString(s) {
		return 0, 0, fieldError(field, "invalid")
	}
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[3:])
	return h, m, nil
}

// wallInstant returns the UTC instant of a local wall-clock time on the civil date day.
func wallInstant(day time.Time, hour, minute int, zone *time.Location) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, zone).UTC()
}

func loadZone(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fieldError("time_zone", "required")
	}
	if name == "Local" {
		return nil, fieldError("time_zone", "invalid")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fieldError("time_zone", "invalid")
	}
	return loc, nil
}

func requiredText(field, raw string, max int) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fieldError(field, "required")
	}
	if utf8.RuneCountInString(s) > max {
		return "", fieldError(field, "too_long")
	}
	return s, nil
}

func optionalText(field string, raw *string, max int) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(s) > max {
		return nil, fieldError(field, "too_long")
	}
	return &s, nil
}

// canonicalRule checks a repeat rule with the task rules, so events and tasks accept the same
// repeats. An empty rule means the event does not repeat.
func canonicalRule(raw *string) (*string, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	rule := strings.TrimSpace(*raw)
	if utf8.RuneCountInString(rule) > maxRuleLen {
		return nil, fieldError("recurrence_rule", "too_long")
	}
	canonical, err := tasks.ValidateRule(rule)
	if err != nil {
		var ruleErr *tasks.FieldError
		if errors.As(err, &ruleErr) {
			return nil, fieldError("recurrence_rule", ruleErr.Reason)
		}
		return nil, err
	}
	return &canonical, nil
}

// normaliseMembers lower-cases and de-duplicates the member IDs. The family check happens in
// the database.
func normaliseMembers(raw []string) ([]string, error) {
	if len(raw) > maxMemberIDs {
		return nil, fieldError("member_ids", "too_long")
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.ToLower(strings.TrimSpace(id))
		if !isUUID(id) {
			return nil, fieldError("member_ids", "invalid")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
