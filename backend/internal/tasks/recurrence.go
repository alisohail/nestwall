package tasks

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	rrule "github.com/teambition/rrule-go"

	"nestwall/backend/internal/platform/ids"
)

// DefaultWindowDays is how far ahead, in days, occurrences of a repeating task are generated.
const DefaultWindowDays = 60

var (
	// ErrNotFound means the task or occurrence does not exist in the family.
	ErrNotFound = errors.New("not found")
	// ErrNotRecurring means the task is a one-off task, so it has no series to edit.
	ErrNotRecurring = errors.New("task does not repeat")
	// ErrSeriesArchived means the repeating definition is archived and generates nothing.
	ErrSeriesArchived = errors.New("repeating task is archived")
)

// weekdayCodes are the RFC 5545 two-letter weekday codes.
var weekdayCodes = map[string]bool{"MO": true, "TU": true, "WE": true, "TH": true, "FR": true, "SA": true, "SU": true}

// supportedFreq lists the RRULE frequencies. Sub-daily frequencies are not supported, because a
// repeat produces one occurrence per calendar day.
var supportedFreq = map[string]bool{"DAILY": true, "WEEKLY": true, "MONTHLY": true, "YEARLY": true}

// allowedParts lists the date-level RRULE parts from RFC 5545 section 3.3.10. Time-of-day parts
// (BYHOUR, BYMINUTE, BYSECOND) are rejected. The library checks how the parts combine.
var allowedParts = map[string]bool{
	"INTERVAL": true, "COUNT": true, "UNTIL": true, "WKST": true,
	"BYMONTH": true, "BYWEEKNO": true, "BYYEARDAY": true, "BYMONTHDAY": true,
	"BYDAY": true, "BYSETPOS": true,
}

// rulePart is one KEY=VALUE pair of an RRULE.
type rulePart struct {
	key   string
	value string
}

func invalidRule() error {
	return &FieldError{Field: "recurrence_rule", Reason: "invalid"}
}

func unsupportedRule() error {
	return &FieldError{Field: "recurrence_rule", Reason: "unsupported"}
}

// parseRule splits an RRULE into its parts, in order. The text may start with "RRULE:". Keys are
// upper-cased and each key may appear once.
func parseRule(rule string) ([]rulePart, error) {
	text := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(rule)), "RRULE:")
	if text == "" {
		return nil, invalidRule()
	}
	seen := make(map[string]bool)
	var parts []rulePart
	for _, item := range strings.Split(text, ";") {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" || value == "" || seen[key] {
			return nil, invalidRule()
		}
		seen[key] = true
		parts = append(parts, rulePart{key: key, value: value})
	}
	return parts, nil
}

// ValidateRule checks that rule is one of the supported Repeat patterns and returns it in
// canonical form. The stored rule has no DTSTART; the series start date lives on the definition.
func ValidateRule(rule string) (string, error) {
	parts, err := parseRule(rule)
	if err != nil {
		return "", err
	}
	values := make(map[string]string, len(parts))
	for _, p := range parts {
		values[p.key] = p.value
	}
	if !supportedFreq[values["FREQ"]] {
		return "", unsupportedRule()
	}
	for _, p := range parts {
		if p.key != "FREQ" && !allowedParts[p.key] {
			return "", unsupportedRule()
		}
	}
	if err := checkRuleShape(values); err != nil {
		return "", err
	}

	canonical := make([]string, 0, len(parts))
	for _, p := range parts {
		canonical = append(canonical, p.key+"="+p.value)
	}
	text := strings.Join(canonical, ";")
	// The library has the final say on UNTIL and bounds. Any rule it rejects is invalid.
	if _, err := libraryRule(text, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		return "", err
	}
	return text, nil
}

// checkRuleShape checks the parts that are invalid on their own. COUNT and UNTIL may not both be
// set. Value ranges and how the parts combine are checked by the library.
func checkRuleShape(values map[string]string) error {
	_, hasCount := values["COUNT"]
	_, hasUntil := values["UNTIL"]
	if hasCount && hasUntil {
		return unsupportedRule()
	}
	if v, ok := values["INTERVAL"]; ok && !atLeast(v, 1) {
		return invalidRule()
	}
	if v, ok := values["COUNT"]; ok && !atLeast(v, 1) {
		return invalidRule()
	}
	if v, ok := values["WKST"]; ok && !weekdayCodes[v] {
		return invalidRule()
	}
	return nil
}

func atLeast(v string, low int) bool {
	n, err := strconv.Atoi(v)
	return err == nil && n >= low
}

// libraryRule parses rule with startsOn as its DTSTART. Dates are UTC midnights, so every
// occurrence is a calendar date with no time zone attached.
func libraryRule(rule string, startsOn time.Time) (*rrule.RRule, error) {
	opt, err := rrule.StrToROptionInLocation(rule, time.UTC)
	if err != nil {
		return nil, invalidRule()
	}
	opt.Dtstart = startsOn
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, invalidRule()
	}
	return r, nil
}

// occurrenceDates returns the calendar dates of rule that fall between from and to, inclusive.
// Counting always starts at startsOn, so COUNT does not depend on the window asked for.
func occurrenceDates(rule string, startsOn, from, to time.Time) ([]time.Time, error) {
	if to.Before(from) {
		return nil, nil
	}
	r, err := libraryRule(rule, startsOn)
	if err != nil {
		return nil, err
	}
	return r.Between(from, to, true), nil
}

// OccurrenceDates is the exported form of occurrenceDates for other packages (the calendar).
func OccurrenceDates(rule string, startsOn, from, to time.Time) ([]time.Time, error) {
	return occurrenceDates(rule, startsOn, from, to)
}

// civilDay parses a YYYY-MM-DD date as UTC midnight.
func civilDay(s string) (time.Time, error) {
	return time.Parse(dateLayout, s)
}

// GenerateWindow creates the occurrences of a repeating definition from today, in the
// definition's time zone, through windowDays later. Dates that are skip dates, or that already
// have an occurrence, are left alone, so running it again creates nothing new. It returns how
// many occurrences were created.
func GenerateWindow(ctx context.Context, tx *sql.Tx, familyID, definitionID string, now time.Time, windowDays int) (int, error) {
	var (
		rule         sql.NullString
		startsOn     string
		zoneName     string
		dueTime      sql.NullString
		expiresAfter sql.NullInt64
		status       string
	)
	err := tx.QueryRowContext(ctx,
		`SELECT recurrence_rule, starts_on, time_zone, due_time, expires_after_minutes, status
		 FROM task_definitions WHERE family_id = ? AND id = ?`,
		familyID, definitionID,
	).Scan(&rule, &startsOn, &zoneName, &dueTime, &expiresAfter, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if !rule.Valid || status != "active" {
		return 0, nil
	}

	zone, err := time.LoadLocation(zoneName)
	if err != nil {
		return 0, err
	}
	first, err := civilDay(startsOn)
	if err != nil {
		return 0, err
	}
	today, err := civilDay(now.In(zone).Format(dateLayout))
	if err != nil {
		return 0, err
	}
	days, err := occurrenceDates(rule.String, first, today, today.AddDate(0, 0, windowDays))
	if err != nil {
		return 0, err
	}

	skips, err := loadSkipDates(ctx, tx, familyID, definitionID)
	if err != nil {
		return 0, err
	}
	members, err := loadDefinitionAssignees(ctx, tx, familyID, definitionID)
	if err != nil {
		return 0, err
	}
	family, _, err := loadSettings(ctx, tx, familyID)
	if err != nil {
		return 0, err
	}
	var due *string
	if dueTime.Valid {
		due = &dueTime.String
	}
	var grace *int
	if expiresAfter.Valid {
		minutes := int(expiresAfter.Int64)
		grace = &minutes
	}

	created := 0
	for _, day := range days {
		date := day.Format(dateLayout)
		if skips[date] {
			continue
		}
		dueAt, err := dueInstant(date, due, zone)
		if err != nil {
			return created, err
		}
		expires := occurrenceExpiry(dueAt, zone, grace, family.MissedAfter)
		inserted, err := insertGeneratedOccurrence(ctx, tx, familyID, definitionID, date, dueAt, expires, members)
		if err != nil {
			return created, err
		}
		if inserted {
			created++
		}
	}
	return created, nil
}

// insertGeneratedOccurrence writes one generated occurrence and a to_do row for each assignee.
// The UNIQUE (task_definition_id, due_at) constraint makes a repeat insert a no-op. It reports
// whether a new occurrence was written.
func insertGeneratedOccurrence(ctx context.Context, tx *sql.Tx, familyID, definitionID, date string, due time.Time, expires *time.Time, members []string) (bool, error) {
	dueText, expiresText := occurrenceTimes(due, expires)
	occurrenceID := ids.New()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO task_occurrences (id, family_id, task_definition_id, due_date, due_at, expires_at, status)
		 VALUES (?, ?, ?, ?, ?, ?, 'scheduled')
		 ON CONFLICT (task_definition_id, due_at) DO NOTHING`,
		occurrenceID, familyID, definitionID, date, dueText, expiresText,
	)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	for _, memberID := range members {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO task_occurrence_assignees (id, family_id, task_occurrence_id, family_member_id, status, version)
			 VALUES (?, ?, ?, ?, 'to_do', 1)`,
			ids.New(), familyID, occurrenceID, memberID,
		); err != nil {
			return false, err
		}
	}
	return true, nil
}

// loadSkipDates returns the skip dates of a definition as a set of YYYY-MM-DD strings.
func loadSkipDates(ctx context.Context, tx *sql.Tx, familyID, definitionID string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT skip_date FROM task_definition_skip_dates WHERE family_id = ? AND task_definition_id = ?`,
		familyID, definitionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	skips := make(map[string]bool)
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			return nil, err
		}
		skips[date] = true
	}
	return skips, rows.Err()
}

// loadDefinitionAssignees returns the family member IDs assigned to a definition.
func loadDefinitionAssignees(ctx context.Context, tx *sql.Tx, familyID, definitionID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT family_member_id FROM task_definition_assignees
		 WHERE family_id = ? AND task_definition_id = ? ORDER BY family_member_id`,
		familyID, definitionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []string
	for rows.Next() {
		var memberID string
		if err := rows.Scan(&memberID); err != nil {
			return nil, err
		}
		members = append(members, memberID)
	}
	return members, rows.Err()
}
