package wall

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/platform/ids"
)

// summaryFx is a paired device in a family with one child and one parent, in UTC. The clock is
// 2026-10-10 09:00 UTC, so "today" is 2026-10-10.
type summaryFx struct {
	wallFixture
	familyID, parentUserID, parentMemberID, childMemberID, deviceID string
	cookie                                                          *http.Cookie
}

func newSummaryFx(t *testing.T) summaryFx {
	t.Helper()
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Hall")
	s := summaryFx{wallFixture: f, deviceID: dev.ID, cookie: cookie}
	queryOne(t, f.db, `SELECT id FROM families LIMIT 1`, &s.familyID)
	queryOne(t, f.db, `SELECT id FROM users WHERE is_child = 0 LIMIT 1`, &s.parentUserID)
	queryOne(t, f.db, `SELECT fm.id FROM family_members fm JOIN roles r ON r.id = fm.role_id WHERE r.name = 'Parent' LIMIT 1`, &s.parentMemberID)
	queryOne(t, f.db, `SELECT fm.id FROM family_members fm JOIN roles r ON r.id = fm.role_id WHERE r.name = 'Child' LIMIT 1`, &s.childMemberID)
	mustExec(t, f.db, `UPDATE families SET timezone = 'UTC' WHERE id = ?`, s.familyID)
	return s
}

func queryOne(t *testing.T, conn *sql.DB, query string, dest ...any) {
	t.Helper()
	if err := conn.QueryRow(query).Scan(dest...); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
}

func intFlag(v bool) int {
	if v {
		return 1
	}
	return 0
}

// setOptions writes the display options the wall reads from its device row.
func (s summaryFx) setOptions(t *testing.T, o summaryOptions) {
	t.Helper()
	mustExec(t, s.db,
		`UPDATE wall_devices SET show_points = ?, show_pictures = ?, show_tomorrow = ?, show_bonus_banner = ? WHERE id = ?`,
		intFlag(o.ShowPoints), intFlag(o.ShowPictures), intFlag(o.ShowTomorrow), intFlag(o.ShowBonusBanner), s.deviceID)
}

func (s summaryFx) fetch(t *testing.T) summaryResponse {
	t.Helper()
	rec := send(s.mux, http.MethodGet, "/api/v1/wall/summary", "", s.cookie, deviceRemote)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out summaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	return out
}

func childByID(t *testing.T, resp summaryResponse, id string) summaryChild {
	t.Helper()
	for _, c := range resp.Children {
		if c.MemberID == id {
			return c
		}
	}
	t.Fatalf("child %s missing from %+v", id, resp.Children)
	return summaryChild{}
}

// wallStamp formats a UTC instant the way the calendar stores it.
func wallStamp(at time.Time) string {
	return at.UTC().Format("2006-01-02 15:04:05.000") + "+00:00"
}

// addCopy adds a one-off task with one assignee. completedAt is set only for submitted, approved
// and rejected copies, and the decision is recorded for approved and rejected ones.
func addCopy(t *testing.T, s summaryFx, memberID, title, dueDate, dueAt, occStatus, status, completedAt string) {
	t.Helper()
	defID, occID := ids.New(), ids.New()
	mustExec(t, s.db,
		`INSERT INTO task_definitions (id, family_id, title, time_zone, starts_on, points, created_by) VALUES (?, ?, ?, 'UTC', ?, 0, ?)`,
		defID, s.familyID, title, dueDate, s.parentUserID)
	mustExec(t, s.db,
		`INSERT INTO task_occurrences (id, family_id, task_definition_id, due_date, due_at, status) VALUES (?, ?, ?, ?, ?, ?)`,
		occID, s.familyID, defID, dueDate, dueAt, occStatus)
	var completed, decided any
	if completedAt != "" {
		completed = completedAt
		if status == "approved" || status == "rejected" {
			decided = completedAt
		}
	}
	mustExec(t, s.db,
		`INSERT INTO task_occurrence_assignees (id, family_id, task_occurrence_id, family_member_id, status, completed_at, decided_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ids.New(), s.familyID, occID, memberID, status, completed, decided)
}

func wallLocalSource(t *testing.T, s summaryFx) string {
	t.Helper()
	mustExec(t, s.db,
		`INSERT INTO calendar_sources (id, family_id, kind, name)
		 SELECT ?, ?, 'local', 'Default' WHERE NOT EXISTS (SELECT 1 FROM calendar_sources WHERE family_id = ? AND kind = 'local')`,
		ids.New(), s.familyID, s.familyID)
	var id string
	if err := s.db.QueryRow(`SELECT id FROM calendar_sources WHERE family_id = ? AND kind = 'local'`, s.familyID).Scan(&id); err != nil {
		t.Fatalf("local source: %v", err)
	}
	return id
}

// addWallEvent adds a timed local event. With no members it is family-wide.
func addWallEvent(t *testing.T, s summaryFx, title, date, start string, minutes int, members ...string) {
	t.Helper()
	starts, err := time.Parse("2006-01-02 15:04", date+" "+start)
	if err != nil {
		t.Fatalf("parse start: %v", err)
	}
	ends := starts.Add(time.Duration(minutes) * time.Minute)
	sourceID := wallLocalSource(t, s)
	eventID := ids.New()
	mustExec(t, s.db,
		`INSERT INTO calendar_events (id, family_id, source_id, title, all_day, time_zone, start_date, start_time,
			duration_minutes, starts_at, ends_at, version, created_by)
		 VALUES (?, ?, ?, ?, 0, 'UTC', ?, ?, ?, ?, ?, 1, ?)`,
		eventID, s.familyID, sourceID, title, date, start, minutes, wallStamp(starts), wallStamp(ends), s.parentUserID)
	for _, m := range members {
		mustExec(t, s.db,
			`INSERT INTO calendar_event_members (id, family_id, event_id, family_member_id) VALUES (?, ?, ?, ?)`,
			ids.New(), s.familyID, eventID, m)
	}
}

func addPoints(t *testing.T, s summaryFx, memberID string, amount int) {
	t.Helper()
	mustExec(t, s.db,
		`INSERT INTO points_ledger (id, family_id, family_member_id, entry_type, amount, reference_type, reference_id, idempotency_key)
		 VALUES (?, ?, ?, 'task_award', ?, 'task_award', ?, ?)`,
		ids.New(), s.familyID, memberID, amount, ids.New(), ids.New())
}

// addBonus adds a bonus task offered to every child. showFrom is nil or a stored timestamp.
func addBonus(t *testing.T, s summaryFx, title string, points int, showFrom any, endsAt string) {
	t.Helper()
	mustExec(t, s.db,
		`INSERT INTO bonus_tasks (id, family_id, title, points, claim_mode, approval_required, show_from, ends_at, created_by)
		 VALUES (?, ?, ?, ?, 'first_come', 1, ?, ?, ?)`,
		ids.New(), s.familyID, title, points, showFrom, endsAt, s.parentUserID)
}

func taskLabels(tasks []summaryTask) string {
	labels := []string{}
	for _, task := range tasks {
		labels = append(labels, task.Title+":"+task.State)
	}
	return strings.Join(labels, ", ")
}

func eventTitles(resp summaryResponse) string {
	titles := []string{}
	for _, e := range resp.Events {
		titles = append(titles, e.Title)
	}
	return strings.Join(titles, ", ")
}

func TestSummaryRefusesWithoutDeviceCredential(t *testing.T) {
	s := newSummaryFx(t)
	cases := map[string]*http.Cookie{"no cookie": nil, "parent session": s.parent, "child session": s.kid}
	for name, cookie := range cases {
		rec := send(s.mux, http.MethodGet, "/api/v1/wall/summary", "", cookie, deviceRemote)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}
	}

	mustExec(t, s.db, `UPDATE wall_devices SET revoked_at = ? WHERE id = ?`, wallStamp(s.clk.now), s.deviceID)
	if rec := send(s.mux, http.MethodGet, "/api/v1/wall/summary", "", s.cookie, deviceRemote); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked device: status %d, want 401", rec.Code)
	}
}

func TestSummaryListsOnlyChildTasksWithOverdueFirstAndDoneLast(t *testing.T) {
	s := newSummaryFx(t)
	const today = "2026-10-10"
	addCopy(t, s, s.childMemberID, "Make bed", "2026-10-08", "2026-10-08 17:00:00+00:00", "past_due", "to_do", "")
	addCopy(t, s, s.childMemberID, "Feed cat", "2026-10-09", "2026-10-09 17:00:00+00:00", "past_due", "rejected", "2026-10-09 18:00:00+00:00")
	addCopy(t, s, s.childMemberID, "Old submitted", "2026-10-09", "2026-10-09 07:00:00+00:00", "past_due", "submitted", "2026-10-09 07:30:00+00:00")
	addCopy(t, s, s.childMemberID, "Missed", "2026-10-08", "2026-10-08 07:00:00+00:00", "missed", "to_do", "")
	addCopy(t, s, s.childMemberID, "Read", today, "2026-10-10 12:00:00+00:00", "scheduled", "to_do", "")
	addCopy(t, s, s.childMemberID, "Lunch dishes", today, "2026-10-10 18:00:00+00:00", "scheduled", "to_do", "")
	addCopy(t, s, s.childMemberID, "Tidy", today, "2026-10-10 08:00:00+00:00", "scheduled", "skipped", "")
	addCopy(t, s, s.childMemberID, "Brush teeth", today, "2026-10-10 07:00:00+00:00", "scheduled", "submitted", "2026-10-10 08:00:00+00:00")
	addCopy(t, s, s.childMemberID, "Pack bag", today, "2026-10-10 06:00:00+00:00", "scheduled", "approved", "2026-10-10 07:30:00+00:00")
	addCopy(t, s, s.parentMemberID, "Parent chore", today, "2026-10-10 10:00:00+00:00", "scheduled", "to_do", "")

	resp := s.fetch(t)
	if resp.FamilyName == "" {
		t.Error("family_name is empty")
	}
	if resp.Date != today || resp.Timezone != "UTC" {
		t.Fatalf("date %q timezone %q, want %q UTC", resp.Date, resp.Timezone, today)
	}
	if len(resp.Children) != 1 {
		t.Fatalf("children = %d, want only the child", len(resp.Children))
	}
	want := "Make bed:overdue, Feed cat:overdue, Read:pending, Lunch dishes:pending, Brush teeth:done, Pack bag:done"
	if got := taskLabels(childByID(t, resp, s.childMemberID).Tasks); got != want {
		t.Fatalf("tasks = %q, want %q", got, want)
	}
	for _, task := range childByID(t, resp, s.childMemberID).Tasks {
		switch task.Title {
		case "Brush teeth":
			if task.CompletedAt == nil || !task.CompletedAt.Equal(time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)) {
				t.Errorf("Brush teeth completed_at = %v, want 08:00 UTC", task.CompletedAt)
			}
		case "Read":
			if task.CompletedAt != nil {
				t.Errorf("pending Read has completed_at %v, want none", task.CompletedAt)
			}
		}
	}
}

func TestSummaryEventsFollowMemberRulesAndWindow(t *testing.T) {
	s := newSummaryFx(t)
	addWallEvent(t, s, "Family dinner", "2026-10-10", "18:00", 60)
	addWallEvent(t, s, "Kid club", "2026-10-11", "10:00", 60, s.childMemberID)
	addWallEvent(t, s, "Parent meeting", "2026-10-10", "12:00", 60, s.parentMemberID)
	addWallEvent(t, s, "Next week", "2026-10-16", "09:00", 60)
	addWallEvent(t, s, "Too far", "2026-10-17", "09:00", 60)

	s.setOptions(t, summaryOptions{ShowTomorrow: false})
	if got, want := eventTitles(s.fetch(t)), "Family dinner, Next week"; got != want {
		t.Fatalf("events without tomorrow = %q, want %q", got, want)
	}

	s.setOptions(t, summaryOptions{ShowTomorrow: true})
	if got, want := eventTitles(s.fetch(t)), "Family dinner, Kid club, Next week"; got != want {
		t.Fatalf("events with tomorrow = %q, want %q", got, want)
	}
}

func TestSummaryPointsFollowDeviceOption(t *testing.T) {
	s := newSummaryFx(t)
	addPoints(t, s, s.childMemberID, 5)

	s.setOptions(t, summaryOptions{ShowPoints: false})
	if child := childByID(t, s.fetch(t), s.childMemberID); child.Points != nil {
		t.Fatalf("points with show_points off = %d, want null", *child.Points)
	}

	s.setOptions(t, summaryOptions{ShowPoints: true})
	child := childByID(t, s.fetch(t), s.childMemberID)
	if child.Points == nil || *child.Points != 5 {
		t.Fatalf("points with show_points on = %v, want 5", child.Points)
	}
}

func TestSummaryEventAttendeesListChildrenOnly(t *testing.T) {
	s := newSummaryFx(t)
	addWallEvent(t, s, "Family dinner", "2026-10-10", "18:00", 60)
	addWallEvent(t, s, "Kid club", "2026-10-10", "10:00", 60, s.childMemberID)

	resp := s.fetch(t)
	if len(resp.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(resp.Events))
	}
	for _, e := range resp.Events {
		switch e.Title {
		case "Family dinner":
			if len(e.Attendees) != 0 {
				t.Errorf("family-wide attendees = %+v, want none", e.Attendees)
			}
		case "Kid club":
			child := childByID(t, resp, s.childMemberID)
			if len(e.Attendees) != 1 || e.Attendees[0].MemberID != s.childMemberID || e.Attendees[0].Name != child.Name || e.Attendees[0].Colour != child.Colour {
				t.Errorf("kid club attendees = %+v, want the child %q in %q", e.Attendees, child.Name, child.Colour)
			}
		}
	}
}

func TestSummaryBonusTasksFollowDeviceOption(t *testing.T) {
	s := newSummaryFx(t)
	addBonus(t, s, "Wash car", 20, nil, "2026-10-10 20:00:00.000+00:00")
	addBonus(t, s, "Ended", 5, nil, "2026-10-10 08:00:00.000+00:00")
	addBonus(t, s, "Later today", 10, "2026-10-10 15:00:00.000+00:00", "2026-10-10 20:00:00.000+00:00")

	s.setOptions(t, summaryOptions{ShowBonusBanner: false})
	if got := s.fetch(t).BonusTasks; len(got) != 0 {
		t.Fatalf("bonus with banner off = %+v, want none", got)
	}

	s.setOptions(t, summaryOptions{ShowBonusBanner: true})
	got := s.fetch(t).BonusTasks
	if len(got) != 1 || got[0].Title != "Wash car" || got[0].Points != 20 || got[0].ClaimMode != "first_come" {
		t.Fatalf("bonus with banner on = %+v, want only Wash car (20 points, first_come)", got)
	}
}
