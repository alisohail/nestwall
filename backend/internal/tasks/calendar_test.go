package tasks

import (
	"context"
	"net/http"
	"reflect"
	"sort"
	"testing"
)

// calendarItems runs the calendar query for family A.
func calendarItems(t *testing.T, f fixture, q CalendarQuery) []CalendarItem {
	t.Helper()
	q.FamilyID = f.a.FamilyID
	items, err := CalendarOccurrences(context.Background(), f.db, q)
	if err != nil {
		t.Fatalf("CalendarOccurrences: %v", err)
	}
	return items
}

func calendarTitles(items []CalendarItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Title)
	}
	return out
}

func TestCalendarOccurrencesWindowAndOrder(t *testing.T) {
	f := newFixture(t)
	kid := addChildMember(t, f, f.a.FamilyID)
	createTask(t, f, f.parent, oneOffBody("Outside early", "2026-10-09", f.parent.memberID))
	createTask(t, f, f.parent, oneOffBody("Bins", "2026-10-11", f.parent.memberID))
	createTask(t, f, f.parent, oneOffBody("Homework", "2026-10-12", f.parent.memberID, kid.memberID))
	createTask(t, f, f.parent, oneOffBody("Outside late", "2026-10-13", f.parent.memberID))

	window := CalendarQuery{From: "2026-10-10", To: "2026-10-12"}
	items := calendarItems(t, f, window)
	if got := calendarTitles(items); !reflect.DeepEqual(got, []string{"Bins", "Homework"}) {
		t.Fatalf("titles = %v, want [Bins Homework] (both window ends inclusive)", got)
	}
	if !items[0].DueAt.Before(items[1].DueAt) {
		t.Fatalf("items not ordered by due time: %v then %v", items[0].DueAt, items[1].DueAt)
	}
	if items[0].DueDate != "2026-10-11" || items[0].OccurrenceID == "" {
		t.Fatalf("first item = %+v", items[0])
	}
	want := []string{f.parent.memberID, kid.memberID}
	sort.Strings(want)
	if !reflect.DeepEqual(items[1].MemberIDs, want) {
		t.Fatalf("members = %v, want %v", items[1].MemberIDs, want)
	}

	if again := calendarItems(t, f, window); !reflect.DeepEqual(again, items) {
		t.Fatalf("repeated query differs:\n%v\n%v", again, items)
	}

	other, err := CalendarOccurrences(context.Background(), f.db,
		CalendarQuery{FamilyID: f.b.FamilyID, From: "2026-10-10", To: "2026-10-12"})
	if err != nil {
		t.Fatalf("CalendarOccurrences other family: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("family B sees %d of family A's tasks", len(other))
	}
}

func TestCalendarOccurrencesMemberFilters(t *testing.T) {
	f := newFixture(t)
	kid := addChildMember(t, f, f.a.FamilyID)
	createTask(t, f, f.parent, oneOffBody("Bins", "2026-10-11", f.parent.memberID))
	createTask(t, f, f.parent, oneOffBody("Homework", "2026-10-11", f.parent.memberID, kid.memberID))
	window := CalendarQuery{From: "2026-10-10", To: "2026-10-11"}

	byMember := window
	byMember.MemberID = kid.memberID
	if got := calendarTitles(calendarItems(t, f, byMember)); !reflect.DeepEqual(got, []string{"Homework"}) {
		t.Fatalf("member filter titles = %v, want [Homework]", got)
	}

	byScope := window
	byScope.ScopeMember = kid.memberID
	if got := calendarTitles(calendarItems(t, f, byScope)); !reflect.DeepEqual(got, []string{"Homework"}) {
		t.Fatalf("scope titles = %v, want [Homework]", got)
	}

	both := byScope
	both.MemberID = f.parent.memberID
	if got := calendarTitles(calendarItems(t, f, both)); !reflect.DeepEqual(got, []string{"Homework"}) {
		t.Fatalf("scope and member titles = %v, want [Homework]", got)
	}

	none := byScope
	none.MemberID = f.other.memberID
	if got := calendarTitles(calendarItems(t, f, none)); len(got) != 0 {
		t.Fatalf("titles for a member not on the task = %v, want none", got)
	}
}

func TestCalendarOccurrencesLeavesOutSkippedCopies(t *testing.T) {
	f := newFixture(t)
	kid := addChildMember(t, f, f.a.FamilyID)
	task := createTask(t, f, f.parent, oneOffBody("Homework", "2026-10-11", f.parent.memberID, kid.memberID))
	window := CalendarQuery{From: "2026-10-11", To: "2026-10-11"}

	execSQL(t, f.db,
		`UPDATE task_occurrence_assignees SET status = 'skipped' WHERE task_occurrence_id = ? AND family_member_id = ?`,
		task.ID, kid.memberID)
	items := calendarItems(t, f, window)
	if len(items) != 1 || !reflect.DeepEqual(items[0].MemberIDs, []string{f.parent.memberID}) {
		t.Fatalf("after skipping the kid's copy: %+v, want one item for the parent only", items)
	}
	kidOnly := window
	kidOnly.MemberID = kid.memberID
	if got := calendarItems(t, f, kidOnly); len(got) != 0 {
		t.Fatalf("skipped copy still matches the member filter: %+v", got)
	}

	execSQL(t, f.db,
		`UPDATE task_occurrence_assignees SET status = 'skipped' WHERE task_occurrence_id = ? AND family_member_id = ?`,
		task.ID, f.parent.memberID)
	if got := calendarItems(t, f, window); len(got) != 0 {
		t.Fatalf("occurrence with every copy skipped is still listed: %+v", got)
	}
}

func TestCalendarOccurrencesSkipsArchivedTasks(t *testing.T) {
	f := newFixture(t)
	task := createTask(t, f, f.parent, oneOffBody("Bins", "2026-10-11", f.parent.memberID))
	window := CalendarQuery{From: "2026-10-11", To: "2026-10-11"}
	if got := calendarItems(t, f, window); len(got) != 1 {
		t.Fatalf("before archive: %d items, want 1", len(got))
	}

	expectStatus(t, f.do(t, f.parent, http.MethodPost, tasksPath+"/"+task.ID+"/archive", nil, ifMatch(task.Version)),
		http.StatusOK)
	if got := calendarItems(t, f, window); len(got) != 0 {
		t.Fatalf("archived task still listed: %+v", got)
	}
}
