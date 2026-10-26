package tasks

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "time/tzdata" // the test binary must find IANA zones on any host

	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

const recZone = "America/Toronto"

// recSetup opens a migrated database and seeds one family with a parent.
func recSetup(t *testing.T) (*sql.DB, testhelper.Family) {
	t.Helper()
	conn := testhelper.OpenDB(t)
	return conn, testhelper.OneFamily(t, conn)
}

// recExec runs a statement that must succeed.
func recExec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// recScalar returns the first column of the first row as text.
func recScalar(t *testing.T, conn *sql.DB, query string, args ...any) string {
	t.Helper()
	var value string
	if err := conn.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}

// recDueDates returns the due dates of a definition's occurrences, in time order.
func recDueDates(t *testing.T, conn *sql.DB, definitionID string) string {
	t.Helper()
	rows, err := conn.Query(`SELECT due_date FROM task_occurrences WHERE task_definition_id = ? ORDER BY due_at`, definitionID)
	if err != nil {
		t.Fatalf("query due dates: %v", err)
	}
	defer rows.Close()
	var dates []string
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			t.Fatalf("scan due date: %v", err)
		}
		dates = append(dates, date)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate due dates: %v", err)
	}
	return strings.Join(dates, ",")
}

// recInTx runs fn in a transaction and commits it.
func recInTx(t *testing.T, conn *sql.DB, fn func(tx *sql.Tx) error) {
	t.Helper()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		t.Fatalf("transaction: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// recSeries inserts an active repeating definition assigned to the parent and returns its ID.
func recSeries(t *testing.T, conn *sql.DB, fam testhelper.Family, rule, startsOn, zone string, dueTime *string) string {
	t.Helper()
	id := ids.New()
	recExec(t, conn,
		`INSERT INTO task_definitions (id, family_id, title, time_zone, starts_on, due_time, recurrence_rule, points, created_by)
		 VALUES (?, ?, 'Series', ?, ?, ?, ?, 1, ?)`,
		id, fam.FamilyID, zone, startsOn, dueTime, rule, fam.ParentUserID,
	)
	recExec(t, conn,
		`INSERT INTO task_definition_assignees (id, family_id, task_definition_id, family_member_id) VALUES (?, ?, ?, ?)`,
		ids.New(), fam.FamilyID, id, fam.MemberID,
	)
	return id
}

// recGenerate runs GenerateWindow at now with the default window and returns the count created.
func recGenerate(t *testing.T, conn *sql.DB, fam testhelper.Family, definitionID string, now time.Time) int {
	t.Helper()
	created := 0
	recInTx(t, conn, func(tx *sql.Tx) error {
		n, err := GenerateWindow(context.Background(), tx, fam.FamilyID, definitionID, now, DefaultWindowDays)
		created = n
		return err
	})
	return created
}

// recOccurrenceID returns the ID of the occurrence of a definition on a date.
func recOccurrenceID(t *testing.T, conn *sql.DB, definitionID, date string) string {
	t.Helper()
	return recScalar(t, conn,
		`SELECT id FROM task_occurrences WHERE task_definition_id = ? AND due_date = ?`,
		definitionID, date,
	)
}

// recMarkSubmitted records a submitted completion on the parent's assignee row.
func recMarkSubmitted(t *testing.T, conn *sql.DB, occurrenceID string, when time.Time) {
	t.Helper()
	recExec(t, conn,
		`UPDATE task_occurrence_assignees SET status = 'submitted', completed_at = ?, version = version + 1
		 WHERE task_occurrence_id = ?`,
		dbTime(when), occurrenceID,
	)
}

// recMarkApproved records an approved completion on the parent's assignee row.
func recMarkApproved(t *testing.T, conn *sql.DB, occurrenceID string, when time.Time) {
	t.Helper()
	recExec(t, conn,
		`UPDATE task_occurrence_assignees SET status = 'approved', completed_at = ?, decided_at = ?, version = version + 1
		 WHERE task_occurrence_id = ?`,
		dbTime(when), dbTime(when), occurrenceID,
	)
}

// recExpand returns the dates of rule from startsOn between from and to, comma-separated.
func recExpand(t *testing.T, rule, startsOn, from, to string) string {
	t.Helper()
	parse := func(s string) time.Time {
		day, err := civilDay(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return day
	}
	days, err := occurrenceDates(rule, parse(startsOn), parse(from), parse(to))
	if err != nil {
		t.Fatalf("expand %q: %v", rule, err)
	}
	out := make([]string, 0, len(days))
	for _, day := range days {
		out = append(out, day.Format(dateLayout))
	}
	return strings.Join(out, ",")
}

func TestValidateRuleAcceptsRepeatOptions(t *testing.T) {
	cases := map[string]string{
		"FREQ=DAILY":                              "FREQ=DAILY",
		"FREQ=DAILY;INTERVAL=3;COUNT=5":           "FREQ=DAILY;INTERVAL=3;COUNT=5",
		"FREQ=DAILY;UNTIL=20260104T235959Z":       "FREQ=DAILY;UNTIL=20260104T235959Z",
		"FREQ=DAILY;BYMONTH=2":                    "FREQ=DAILY;BYMONTH=2",
		"FREQ=WEEKLY":                             "FREQ=WEEKLY",
		"FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TH":      "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TH",
		"FREQ=MONTHLY;BYMONTHDAY=-1":              "FREQ=MONTHLY;BYMONTHDAY=-1",
		"FREQ=MONTHLY;BYMONTHDAY=1,15":            "FREQ=MONTHLY;BYMONTHDAY=1,15",
		"FREQ=MONTHLY;BYDAY=TU":                   "FREQ=MONTHLY;BYDAY=TU",
		"FREQ=MONTHLY;BYDAY=FR;BYSETPOS=-1":       "FREQ=MONTHLY;BYDAY=FR;BYSETPOS=-1",
		"FREQ=MONTHLY;BYDAY=1MO,-1FR":             "FREQ=MONTHLY;BYDAY=1MO,-1FR",
		"FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=14":     "FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=14",
		"FREQ=YEARLY;BYMONTH=1,7;BYMONTHDAY=1,15": "FREQ=YEARLY;BYMONTH=1,7;BYMONTHDAY=1,15",
		"FREQ=YEARLY;BYYEARDAY=1,100":             "FREQ=YEARLY;BYYEARDAY=1,100",
		"FREQ=YEARLY;BYWEEKNO=20;BYDAY=MO":        "FREQ=YEARLY;BYWEEKNO=20;BYDAY=MO",
		"RRULE:freq=weekly;byday=mo":              "FREQ=WEEKLY;BYDAY=MO",
	}
	for input, want := range cases {
		got, err := ValidateRule(input)
		if err != nil {
			t.Errorf("ValidateRule(%q): unexpected error %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("ValidateRule(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestValidateRuleRejectsOutsideRepeatOptions(t *testing.T) {
	cases := []struct {
		rule   string
		reason string
	}{
		{"", "invalid"},
		{"INTERVAL=2", "unsupported"},
		{"FREQ=HOURLY", "unsupported"},
		{"FREQ=MINUTELY;INTERVAL=15", "unsupported"},
		{"FREQ=DAILY;BYHOUR=9", "unsupported"},
		{"FREQ=DAILY;BYMINUTE=30", "unsupported"},
		{"FREQ=WEEKLY;BYDAY=XX", "invalid"},
		{"FREQ=MONTHLY;BYMONTHDAY=0", "invalid"},
		{"FREQ=MONTHLY;BYMONTHDAY=32", "invalid"},
		{"FREQ=DAILY;COUNT=3;UNTIL=20260104T235959Z", "unsupported"},
		{"FREQ=DAILY;INTERVAL=0", "invalid"},
		{"FREQ=DAILY;FREQ=WEEKLY", "invalid"},
		{"FREQ=YEARLY;BYMONTH=13;BYMONTHDAY=1", "invalid"},
	}
	for _, tc := range cases {
		_, err := ValidateRule(tc.rule)
		var fe *FieldError
		if !errors.As(err, &fe) {
			t.Errorf("ValidateRule(%q): got %v, want a FieldError", tc.rule, err)
			continue
		}
		if fe.Reason != tc.reason {
			t.Errorf("ValidateRule(%q): reason %q, want %q", tc.rule, fe.Reason, tc.reason)
		}
	}
}

func TestOccurrenceDatesFollowRepeatOptions(t *testing.T) {
	cases := []struct {
		name     string
		rule     string
		startsOn string
		from     string
		to       string
		want     string
	}{
		{"every 3 days", "FREQ=DAILY;INTERVAL=3", "2026-01-01", "2026-01-01", "2026-01-10",
			"2026-01-01,2026-01-04,2026-01-07,2026-01-10"},
		{"every 2 weeks on Monday and Thursday", "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,TH", "2026-01-01", "2026-01-01", "2026-01-31",
			"2026-01-01,2026-01-12,2026-01-15,2026-01-26,2026-01-29"},
		{"weekdays preset", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", "2026-01-03", "2026-01-03", "2026-01-09",
			"2026-01-05,2026-01-06,2026-01-07,2026-01-08,2026-01-09"},
		{"weekends preset", "FREQ=WEEKLY;BYDAY=SA,SU", "2026-01-03", "2026-01-03", "2026-01-11",
			"2026-01-03,2026-01-04,2026-01-10,2026-01-11"},
		{"every day preset", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR,SA,SU", "2026-01-01", "2026-01-01", "2026-01-03",
			"2026-01-01,2026-01-02,2026-01-03"},
		{"monthly on the 15th", "FREQ=MONTHLY;BYMONTHDAY=15", "2026-01-15", "2026-01-01", "2026-03-31",
			"2026-01-15,2026-02-15,2026-03-15"},
		{"monthly on the last day", "FREQ=MONTHLY;BYMONTHDAY=-1", "2026-01-31", "2026-01-01", "2026-03-31",
			"2026-01-31,2026-02-28,2026-03-31"},
		{"day 31 skips short months", "FREQ=MONTHLY;BYMONTHDAY=31", "2026-01-31", "2026-01-01", "2026-06-30",
			"2026-01-31,2026-03-31,2026-05-31"},
		{"second Tuesday", "FREQ=MONTHLY;BYDAY=TU;BYSETPOS=2", "2026-01-13", "2026-01-01", "2026-03-31",
			"2026-01-13,2026-02-10,2026-03-10"},
		{"last Friday", "FREQ=MONTHLY;BYDAY=FR;BYSETPOS=-1", "2026-01-30", "2026-01-01", "2026-03-31",
			"2026-01-30,2026-02-27,2026-03-27"},
		{"yearly on 14 March", "FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=14", "2026-03-14", "2026-01-01", "2028-12-31",
			"2026-03-14,2027-03-14,2028-03-14"},
		{"ends on a date", "FREQ=DAILY;UNTIL=20260104T235959Z", "2026-01-01", "2026-01-01", "2026-01-10",
			"2026-01-01,2026-01-02,2026-01-03,2026-01-04"},
		{"ends after a count", "FREQ=DAILY;COUNT=3", "2026-01-01", "2026-01-01", "2026-01-10",
			"2026-01-01,2026-01-02,2026-01-03"},
		{"count is counted from the start, not the window", "FREQ=DAILY;COUNT=3", "2026-01-01", "2026-01-02", "2026-01-10",
			"2026-01-02,2026-01-03"},
		{"weekly without a day follows the start date", "FREQ=WEEKLY", "2026-01-03", "2026-01-03", "2026-01-17",
			"2026-01-03,2026-01-10,2026-01-17"},
		{"every Tuesday of the month", "FREQ=MONTHLY;BYDAY=TU", "2026-01-01", "2026-01-01", "2026-02-28",
			"2026-01-06,2026-01-13,2026-01-20,2026-01-27,2026-02-03,2026-02-10,2026-02-17,2026-02-24"},
		{"last weekday of the month", "FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1", "2026-01-01", "2026-01-01", "2026-02-28",
			"2026-01-30,2026-02-27"},
		{"daily in February only", "FREQ=DAILY;BYMONTH=2", "2026-01-01", "2026-01-01", "2026-02-03",
			"2026-02-01,2026-02-02,2026-02-03"},
		{"yearly on two months", "FREQ=YEARLY;BYMONTH=1,7;BYMONTHDAY=1", "2026-01-01", "2026-01-01", "2026-08-01",
			"2026-01-01,2026-07-01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recExpand(t, tc.rule, tc.startsOn, tc.from, tc.to); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestGenerateWindowIsIdempotent(t *testing.T) {
	conn, fam := recSetup(t)
	nine := "09:00"
	series := recSeries(t, conn, fam, "FREQ=DAILY", "2026-01-01", recZone, &nine)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Jan 1 to Mar 2 inclusive is 61 days: today plus the 60-day window.
	if got := recGenerate(t, conn, fam, series, now); got != 61 {
		t.Fatalf("first run created %d, want 61", got)
	}
	if got := recGenerate(t, conn, fam, series, now); got != 0 {
		t.Fatalf("second run created %d, want 0", got)
	}
	if got := recScalar(t, conn, `SELECT COUNT(*) FROM task_occurrences WHERE task_definition_id = ?`, series); got != "61" {
		t.Errorf("occurrences = %s, want 61", got)
	}
	if got := recScalar(t, conn,
		`SELECT COUNT(*) FROM task_occurrence_assignees oa JOIN task_occurrences o ON o.id = oa.task_occurrence_id
		 WHERE o.task_definition_id = ? AND oa.status = 'to_do'`, series); got != "61" {
		t.Errorf("to_do assignees = %s, want 61", got)
	}
	// 09:00 in Toronto on Jan 3 (EST, UTC-5) is 14:00 UTC.
	if got := recScalar(t, conn,
		`SELECT CAST(due_at AS TEXT) FROM task_occurrences WHERE task_definition_id = ? AND due_date = '2026-01-03'`, series); got != "2026-01-03 14:00:00.000+00:00" {
		t.Errorf("due_at = %s", got)
	}
}

func TestGenerateWindowDefaultsDueToEndOfDay(t *testing.T) {
	conn, fam := recSetup(t)
	series := recSeries(t, conn, fam, "FREQ=DAILY", "2026-01-01", recZone, nil)
	recGenerate(t, conn, fam, series, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	// 23:59:59 in Toronto on Jan 3 is 04:59:59 UTC on Jan 4.
	if got := recScalar(t, conn,
		`SELECT CAST(due_at AS TEXT) FROM task_occurrences WHERE task_definition_id = ? AND due_date = '2026-01-03'`, series); got != "2026-01-04 04:59:59.000+00:00" {
		t.Errorf("due_at = %s", got)
	}
}

func TestGenerateWindowSkipsSkipDates(t *testing.T) {
	conn, fam := recSetup(t)
	series := recSeries(t, conn, fam, "FREQ=DAILY", "2026-01-01", recZone, nil)
	recExec(t, conn,
		`INSERT INTO task_definition_skip_dates (family_id, task_definition_id, skip_date) VALUES (?, ?, '2026-01-02')`,
		fam.FamilyID, series,
	)
	if got := recGenerate(t, conn, fam, series, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)); got != 60 {
		t.Fatalf("created %d, want 60", got)
	}
	if got := recScalar(t, conn,
		`SELECT COUNT(*) FROM task_occurrences WHERE task_definition_id = ? AND due_date = '2026-01-02'`, series); got != "0" {
		t.Errorf("skip date generated %s occurrences", got)
	}
}

func TestDailyOccurrencesAcrossDST(t *testing.T) {
	conn, fam := recSetup(t)
	twoThirty := "02:30"

	// Spring forward: 2026-03-08 02:00 to 03:00 does not exist in Toronto.
	spring := recSeries(t, conn, fam, "FREQ=DAILY", "2026-03-06", recZone, &twoThirty)
	recGenerate(t, conn, fam, spring, time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	// Before the change, 02:30 EST is 07:30 UTC.
	if got := recScalar(t, conn,
		`SELECT CAST(due_at AS TEXT) FROM task_occurrences WHERE task_definition_id = ? AND due_date = '2026-03-06'`, spring); got != "2026-03-06 07:30:00.000+00:00" {
		t.Errorf("before change: due_at = %s", got)
	}
	// After the change, 02:30 EDT is 06:30 UTC.
	if got := recScalar(t, conn,
		`SELECT CAST(due_at AS TEXT) FROM task_occurrences WHERE task_definition_id = ? AND due_date = '2026-03-09'`, spring); got != "2026-03-09 06:30:00.000+00:00" {
		t.Errorf("after change: due_at = %s", got)
	}

	// Fall back: 2026-11-01 01:00 to 02:00 happens twice in Toronto. An ambiguous local
	// time resolves to the first occurrence (EDT, UTC-4), so 01:30 is 05:30 UTC.
	oneThirty := "01:30"
	fall := recSeries(t, conn, fam, "FREQ=DAILY", "2026-10-30", recZone, &oneThirty)
	recGenerate(t, conn, fam, fall, time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	if got := recScalar(t, conn,
		`SELECT COUNT(*) FROM task_occurrences WHERE task_definition_id = ? AND due_date = '2026-11-01'`, fall); got != "1" {
		t.Errorf("fall back: %s occurrences on 2026-11-01, want 1", got)
	}
	if got := recScalar(t, conn,
		`SELECT CAST(due_at AS TEXT) FROM task_occurrences WHERE task_definition_id = ? AND due_date = '2026-11-01'`, fall); got != "2026-11-01 05:30:00.000+00:00" {
		t.Errorf("fall back: due_at = %s", got)
	}
}

func TestDetachOccurrenceKeepsOtherOccurrences(t *testing.T) {
	conn, fam := recSetup(t)
	nine := "09:00"
	series := recSeries(t, conn, fam, "FREQ=DAILY", "2026-01-01", recZone, &nine)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	recGenerate(t, conn, fam, series, now)

	target := recOccurrenceID(t, conn, series, "2026-01-03")
	var detached string
	recInTx(t, conn, func(tx *sql.Tx) error {
		id, err := DetachOccurrence(context.Background(), tx, fam.FamilyID, fam.ParentUserID, target, time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC))
		detached = id
		return err
	})

	if got := recScalar(t, conn, `SELECT task_definition_id FROM task_occurrences WHERE id = ?`, target); got != detached {
		t.Errorf("occurrence belongs to %s, want detached %s", got, detached)
	}
	if got := recScalar(t, conn, `SELECT COALESCE(recurrence_rule, 'NULL') FROM task_definitions WHERE id = ?`, detached); got != "NULL" {
		t.Errorf("detached rule = %s, want NULL (one-off)", got)
	}
	if got := recScalar(t, conn, `SELECT starts_on FROM task_definitions WHERE id = ?`, detached); got != "2026-01-03" {
		t.Errorf("detached starts_on = %s", got)
	}
	if got := recScalar(t, conn, `SELECT COUNT(*) FROM task_occurrences WHERE task_definition_id = ?`, series); got != "60" {
		t.Errorf("series occurrences = %s, want 60", got)
	}
	if got := recScalar(t, conn, `SELECT COUNT(*) FROM task_definition_skip_dates WHERE task_definition_id = ? AND skip_date = '2026-01-03'`, series); got != "1" {
		t.Errorf("skip date rows = %s, want 1", got)
	}
	// The skip date stops regeneration from recreating the detached day.
	if got := recGenerate(t, conn, fam, series, now); got != 0 {
		t.Errorf("regeneration created %d, want 0", got)
	}
}

func TestSplitSeriesLeavesHistoryAlone(t *testing.T) {
	conn, fam := recSetup(t)
	nine := "09:00"
	series := recSeries(t, conn, fam, "FREQ=DAILY", "2026-01-01", recZone, &nine)
	recGenerate(t, conn, fam, series, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))

	jan1 := recOccurrenceID(t, conn, series, "2026-01-01")
	jan2 := recOccurrenceID(t, conn, series, "2026-01-02")
	recMarkApproved(t, conn, jan1, time.Date(2026, 1, 1, 15, 0, 0, 0, time.UTC))
	recMarkApproved(t, conn, jan2, time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC))
	versionBefore := recScalar(t, conn, `SELECT version FROM task_occurrence_assignees WHERE task_occurrence_id = ?`, jan1)

	jan5 := recOccurrenceID(t, conn, series, "2026-01-05")
	var newID string
	recInTx(t, conn, func(tx *sql.Tx) error {
		id, err := SplitSeries(context.Background(), tx, fam.FamilyID, fam.ParentUserID, jan5, "FREQ=WEEKLY;BYDAY=MO", time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC))
		newID = id
		return err
	})

	// The earlier part keeps its four days and is capped at COUNT=4.
	if got := recScalar(t, conn, `SELECT recurrence_rule FROM task_definitions WHERE id = ?`, series); got != "FREQ=DAILY;COUNT=4" {
		t.Errorf("earlier rule = %s, want FREQ=DAILY;COUNT=4", got)
	}
	if got := recDueDates(t, conn, series); got != "2026-01-01,2026-01-02,2026-01-03,2026-01-04" {
		t.Errorf("earlier occurrences = %s", got)
	}
	if got := recScalar(t, conn, `SELECT status FROM task_occurrence_assignees WHERE task_occurrence_id = ?`, jan1); got != "approved" {
		t.Errorf("Jan 1 status = %s, want approved", got)
	}
	if got := recScalar(t, conn, `SELECT version FROM task_occurrence_assignees WHERE task_occurrence_id = ?`, jan1); got != versionBefore {
		t.Errorf("Jan 1 version changed from %s to %s", versionBefore, got)
	}

	// The new definition starts on the split date and follows the new rule.
	if got := recScalar(t, conn, `SELECT recurrence_rule || ' ' || starts_on FROM task_definitions WHERE id = ?`, newID); got != "FREQ=WEEKLY;BYDAY=MO 2026-01-05" {
		t.Errorf("new definition = %s", got)
	}
	want := "2026-01-05,2026-01-12,2026-01-19,2026-01-26,2026-02-02,2026-02-09,2026-02-16,2026-02-23,2026-03-02"
	if got := recDueDates(t, conn, newID); got != want {
		t.Errorf("new occurrences = %s, want %s", got, want)
	}
}

func TestReplaceFutureRuleKeepsProgressAndRegenerates(t *testing.T) {
	conn, fam := recSetup(t)
	nine := "09:00"
	series := recSeries(t, conn, fam, "FREQ=DAILY", "2026-01-01", recZone, &nine)
	recGenerate(t, conn, fam, series, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))

	jan2 := recOccurrenceID(t, conn, series, "2026-01-02")
	jan12 := recOccurrenceID(t, conn, series, "2026-01-12")
	recMarkApproved(t, conn, jan2, time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC))
	recMarkSubmitted(t, conn, jan12, time.Date(2026, 1, 12, 15, 0, 0, 0, time.UTC))

	now := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	jan20 := recOccurrenceID(t, conn, series, "2026-01-20")
	recInTx(t, conn, func(tx *sql.Tx) error {
		return ReplaceFutureRule(context.Background(), tx, fam.FamilyID, fam.ParentUserID, jan20, "FREQ=WEEKLY;BYDAY=MO", now)
	})

	if got := recScalar(t, conn, `SELECT recurrence_rule || ' ' || version FROM task_definitions WHERE id = ?`, series); got != "FREQ=WEEKLY;BYDAY=MO 2" {
		t.Errorf("series = %s, want FREQ=WEEKLY;BYDAY=MO 2", got)
	}
	if got := recScalar(t, conn, `SELECT status FROM task_occurrence_assignees WHERE task_occurrence_id = ?`, jan2); got != "approved" {
		t.Errorf("Jan 2 status = %s, want approved", got)
	}
	if got := recScalar(t, conn, `SELECT status FROM task_occurrence_assignees WHERE task_occurrence_id = ?`, jan12); got != "submitted" {
		t.Errorf("Jan 12 status = %s, want submitted", got)
	}
	// Past days stay. Untouched future days are removed and regenerated. Jan 12 keeps its
	// submission, and Monday Jan 5 is generated again.
	want := "2026-01-01,2026-01-02,2026-01-03,2026-01-04,2026-01-05,2026-01-12,2026-01-19,2026-01-26,2026-02-02,2026-02-09,2026-02-16,2026-02-23,2026-03-02"
	if got := recDueDates(t, conn, series); got != want {
		t.Errorf("occurrences = %s, want %s", got, want)
	}
}

func TestSeriesEditsAuditTheOccurrence(t *testing.T) {
	nine := "09:00"
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := map[string]func(tx *sql.Tx, fam testhelper.Family, occ string) error{
		"task.occurrence_detached": func(tx *sql.Tx, fam testhelper.Family, occ string) error {
			_, err := DetachOccurrence(context.Background(), tx, fam.FamilyID, fam.ParentUserID, occ, now)
			return err
		},
		"task.series_split": func(tx *sql.Tx, fam testhelper.Family, occ string) error {
			_, err := SplitSeries(context.Background(), tx, fam.FamilyID, fam.ParentUserID, occ, "FREQ=WEEKLY;BYDAY=MO", now)
			return err
		},
		"task.series_rule_changed": func(tx *sql.Tx, fam testhelper.Family, occ string) error {
			return ReplaceFutureRule(context.Background(), tx, fam.FamilyID, fam.ParentUserID, occ, "FREQ=WEEKLY;BYDAY=MO", now)
		},
	}
	for action, run := range cases {
		t.Run(action, func(t *testing.T) {
			conn, fam := recSetup(t)
			series := recSeries(t, conn, fam, "FREQ=DAILY", "2026-01-01", recZone, &nine)
			recGenerate(t, conn, fam, series, now)
			occ := recOccurrenceID(t, conn, series, "2026-01-05")
			recInTx(t, conn, func(tx *sql.Tx) error { return run(tx, fam, occ) })

			got := recScalar(t, conn, `SELECT COUNT(*) FROM audit_logs WHERE family_id = ? AND action = ? AND target_type = 'task' AND target_id = ?`, fam.FamilyID, action, occ)
			if got != "1" {
				t.Errorf("audit rows for %s on the occurrence = %s, want 1", action, got)
			}
		})
	}
}
