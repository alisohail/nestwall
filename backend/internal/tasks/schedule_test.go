package tasks

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "time/tzdata" // the test binary must find IANA zones on any host

	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

// schSpec describes a repeating definition for the scheduler tests. Nil fields are left unset.
type schSpec struct {
	rule         string
	startsOn     string
	dueTime      *string
	points       int
	expires      *int
	penaltyValue *int
	penaltyUnit  *string
}

// schDefinition inserts an active repeating definition assigned to the parent and returns its ID.
func schDefinition(t *testing.T, conn *sql.DB, fam testhelper.Family, spec schSpec) string {
	t.Helper()
	id := ids.New()
	recExec(t, conn,
		`INSERT INTO task_definitions (id, family_id, title, time_zone, starts_on, due_time, recurrence_rule,
			points, expires_after_minutes, penalty_value, penalty_unit, created_by)
		 VALUES (?, ?, 'Scheduled', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, fam.FamilyID, recZone, spec.startsOn, spec.dueTime, spec.rule, spec.points,
		spec.expires, spec.penaltyValue, spec.penaltyUnit, fam.ParentUserID,
	)
	recExec(t, conn,
		`INSERT INTO task_definition_assignees (id, family_id, task_definition_id, family_member_id) VALUES (?, ?, ?, ?)`,
		ids.New(), fam.FamilyID, id, fam.MemberID,
	)
	return id
}

// schRun runs the scheduler at now and fails the test on error.
func schRun(t *testing.T, conn *sql.DB, now string) ScheduleResult {
	t.Helper()
	result, err := RunSchedule(context.Background(), conn, schAt(t, now))
	if err != nil {
		t.Fatalf("RunSchedule at %s: %v", now, err)
	}
	return result
}

// schAt parses an RFC 3339 instant.
func schAt(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return at
}

// schStatus returns an occurrence's schedule status.
func schStatus(t *testing.T, conn *sql.DB, occurrenceID string) string {
	t.Helper()
	return recScalar(t, conn, `SELECT status FROM task_occurrences WHERE id = ?`, occurrenceID)
}

// schIntents returns how many penalty intents exist for an occurrence.
func schIntents(t *testing.T, conn *sql.DB, occurrenceID string) string {
	t.Helper()
	return recScalar(t, conn, `SELECT COUNT(*) FROM task_penalty_intents WHERE task_occurrence_id = ?`, occurrenceID)
}

func schInt(v int) *int       { return &v }
func schStr(v string) *string { return &v }

func TestSchedulePastDueAtDueInstant(t *testing.T) {
	conn, fam := recSetup(t)
	def := schDefinition(t, conn, fam, schSpec{
		rule: "FREQ=DAILY", startsOn: "2026-10-10", dueTime: schStr("08:00"), points: 1,
	})
	// 08:00 Toronto on 2026-10-10 is 12:00 UTC (EDT).
	schRun(t, conn, "2026-10-10T11:59:59Z")
	occ := recOccurrenceID(t, conn, def, "2026-10-10")
	if got := schStatus(t, conn, occ); got != "scheduled" {
		t.Fatalf("one second before due: status = %q, want scheduled", got)
	}
	schRun(t, conn, "2026-10-10T12:00:00Z")
	if got := schStatus(t, conn, occ); got != "past_due" {
		t.Fatalf("at due instant: status = %q, want past_due", got)
	}
}

func TestScheduleMissedAtExpiryInstant(t *testing.T) {
	conn, fam := recSetup(t)
	// Due 12:00 UTC, grace 120 minutes, so expiry is 14:00 UTC. No penalty is configured.
	def := schDefinition(t, conn, fam, schSpec{
		rule: "FREQ=DAILY", startsOn: "2026-10-10", dueTime: schStr("08:00"), points: 1, expires: schInt(120),
	})
	schRun(t, conn, "2026-10-10T13:59:59Z")
	occ := recOccurrenceID(t, conn, def, "2026-10-10")
	if got := schStatus(t, conn, occ); got != "past_due" {
		t.Fatalf("one second before expiry: status = %q, want past_due", got)
	}
	result := schRun(t, conn, "2026-10-10T14:00:00Z")
	if got := schStatus(t, conn, occ); got != "missed" {
		t.Fatalf("at expiry: status = %q, want missed", got)
	}
	if result.Missed < 1 {
		t.Fatalf("result.Missed = %d, want at least 1", result.Missed)
	}
	if got := schIntents(t, conn, occ); got != "0" {
		t.Fatalf("intents without a penalty = %s, want 0", got)
	}
}

func TestSchedulePenaltyResolvesToPoints(t *testing.T) {
	cases := []struct {
		name   string
		points int
		value  int
		unit   string
		want   string
	}{
		{"percent rounds half up", 15, 50, "percent", "8"},
		{"percent 33 of 15", 15, 33, "percent", "5"},
		{"fixed points", 15, 3, "points", "3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, fam := recSetup(t)
			def := schDefinition(t, conn, fam, schSpec{
				rule: "FREQ=DAILY", startsOn: "2026-10-10", dueTime: schStr("08:00"), points: tc.points,
				expires: schInt(120), penaltyValue: schInt(tc.value), penaltyUnit: schStr(tc.unit),
			})
			schRun(t, conn, "2026-10-10T14:00:00Z")
			occ := recOccurrenceID(t, conn, def, "2026-10-10")

			if got := schStatus(t, conn, occ); got != "missed" {
				t.Fatalf("status = %q, want missed", got)
			}
			got := recScalar(t, conn,
				`SELECT penalty_points FROM task_penalty_intents WHERE task_occurrence_id = ? AND family_member_id = ?`,
				occ, fam.MemberID)
			if got != tc.want {
				t.Fatalf("penalty_points = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestSchedulePenaltyIsRecordedOnce(t *testing.T) {
	conn, fam := recSetup(t)
	def := schDefinition(t, conn, fam, schSpec{
		rule: "FREQ=DAILY", startsOn: "2026-10-10", dueTime: schStr("08:00"), points: 15,
		expires: schInt(120), penaltyValue: schInt(50), penaltyUnit: schStr("percent"),
	})
	// Past due first, then missed, which is the normal path of a worker that runs every minute.
	schRun(t, conn, "2026-10-10T12:00:00Z")
	occ := recOccurrenceID(t, conn, def, "2026-10-10")
	first := schRun(t, conn, "2026-10-10T14:00:00Z")
	if first.Penalties != 1 {
		t.Fatalf("first expiry penalties = %d, want 1", first.Penalties)
	}

	// A restart, and then later runs, record nothing new.
	again := schRun(t, conn, "2026-10-10T14:00:00Z")
	later := schRun(t, conn, "2026-10-10T15:30:00Z")
	if again.Missed != 0 || again.Penalties != 0 || again.PastDue != 0 || again.Generated != 0 {
		t.Fatalf("rerun at expiry changed something: %+v", again)
	}
	if later.Missed != 0 || later.Penalties != 0 {
		t.Fatalf("later run changed a missed occurrence: %+v", later)
	}
	if got := schIntents(t, conn, occ); got != "1" {
		t.Fatalf("intents = %s, want 1", got)
	}
	// One change to past_due and one to missed, and nothing since.
	if got := recScalar(t, conn, `SELECT version FROM task_occurrences WHERE id = ?`, occ); got != "3" {
		t.Fatalf("occurrence version = %s, want 3", got)
	}
}

func TestScheduleSubmittedBeforeExpiryEarnsNoPenalty(t *testing.T) {
	conn, fam := recSetup(t)
	def := schDefinition(t, conn, fam, schSpec{
		rule: "FREQ=DAILY", startsOn: "2026-10-10", dueTime: schStr("08:00"), points: 10,
		expires: schInt(120), penaltyValue: schInt(50), penaltyUnit: schStr("percent"),
	})
	// Generate the occurrence, then mark it done late: after due, before expiry.
	schRun(t, conn, "2026-10-10T13:00:00Z")
	occ := recOccurrenceID(t, conn, def, "2026-10-10")
	recMarkSubmitted(t, conn, occ, schAt(t, "2026-10-10T13:00:00Z"))

	// The scheduler must not touch the child's row.
	schRun(t, conn, "2026-10-10T14:00:00Z")

	if got := schStatus(t, conn, occ); got != "missed" {
		t.Fatalf("occurrence status = %q, want missed", got)
	}
	if got := schIntents(t, conn, occ); got != "0" {
		t.Fatalf("intents = %s, want 0 for a submitted assignee", got)
	}
	if got := recScalar(t, conn,
		`SELECT status FROM task_occurrence_assignees WHERE task_occurrence_id = ?`, occ); got != "submitted" {
		t.Fatalf("assignee status = %q, want submitted", got)
	}
}

func TestScheduleGraceEndsAtStoredExpiryAcrossFallBack(t *testing.T) {
	conn, fam := recSetup(t)
	// 01:30 on 2026-11-01 happens twice in Toronto. Go resolves it to the first (EDT, UTC-4), so
	// the due instant is 05:30 UTC. A 60 minute grace is an absolute duration, so it ends at 06:30
	// UTC, which is 01:30 EST.
	def := schDefinition(t, conn, fam, schSpec{
		rule: "FREQ=DAILY", startsOn: "2026-11-01", dueTime: schStr("01:30"), points: 1, expires: schInt(60),
	})
	schRun(t, conn, "2026-10-31T12:00:00Z")
	occ := recOccurrenceID(t, conn, def, "2026-11-01")

	dueAt := recScalar(t, conn, `SELECT CAST(due_at AS TEXT) FROM task_occurrences WHERE id = ?`, occ)
	expiresAt := recScalar(t, conn, `SELECT CAST(expires_at AS TEXT) FROM task_occurrences WHERE id = ?`, occ)
	if dueAt != "2026-11-01 05:30:00.000+00:00" || expiresAt != "2026-11-01 06:30:00.000+00:00" {
		t.Fatalf("due_at = %q, expires_at = %q, want 05:30Z and 06:30Z", dueAt, expiresAt)
	}

	schRun(t, conn, "2026-11-01T06:29:59Z")
	if got := schStatus(t, conn, occ); got != "past_due" {
		t.Fatalf("one second before stored expiry: status = %q, want past_due", got)
	}
	schRun(t, conn, "2026-11-01T06:30:00Z")
	if got := schStatus(t, conn, occ); got != "missed" {
		t.Fatalf("at stored expiry: status = %q, want missed", got)
	}
}

func TestScheduleGenerationIsIdempotent(t *testing.T) {
	conn, fam := recSetup(t)
	schDefinition(t, conn, fam, schSpec{
		rule: "FREQ=DAILY", startsOn: "2026-10-10", dueTime: schStr("08:00"), points: 1,
	})

	first := schRun(t, conn, "2026-10-10T07:00:00Z")
	if first.Generated != DefaultWindowDays+1 {
		t.Fatalf("first generated = %d, want %d", first.Generated, DefaultWindowDays+1)
	}
	second := schRun(t, conn, "2026-10-10T07:00:00Z")
	if second.Generated != 0 {
		t.Fatalf("second generated = %d, want 0", second.Generated)
	}
}
