package tasks

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// utc returns an instant on 2026-10-day at the given hour in UTC.
func utc(day, hour int) time.Time {
	return time.Date(2026, 10, day, hour, 0, 0, 0, time.UTC)
}

// myTasksGet calls GET /api/v1/me/tasks as the actor and returns the decoded response.
func myTasksGet(t *testing.T, f fixture, a actor) MyTasksResponse {
	t.Helper()
	rec := f.do(t, a, http.MethodGet, "/api/v1/me/tasks", nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var out MyTasksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode my tasks %q: %v", rec.Body.String(), err)
	}
	return out
}

// myComingPath and myDonePath are the paged endpoints for the coming-up and done lists.
const (
	myComingPath = "/api/v1/me/tasks/coming"
	myDonePath   = "/api/v1/me/tasks/done"
)

// myListGet reads one page of a paged list. path may carry a query string.
func myListGet(t *testing.T, f fixture, a actor, path string) myListPage {
	t.Helper()
	rec := f.do(t, a, http.MethodGet, path, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var out myListPage
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode page %q: %v", rec.Body.String(), err)
	}
	return out
}

// walkPages follows next_cursor from path at the given page size and returns every task ID in order,
// with the number of pages read.
func walkPages(t *testing.T, f fixture, a actor, path, limit string) ([]string, int) {
	t.Helper()
	var ids []string
	cursor := ""
	pages := 0
	for {
		target := path + "?limit=" + limit
		if cursor != "" {
			target += "&cursor=" + cursor
		}
		page := myListGet(t, f, a, target)
		ids = append(ids, itemIDs(page.Tasks)...)
		pages++
		if page.NextCursor == nil {
			return ids, pages
		}
		if pages > 10 {
			t.Fatal("pagination did not end")
		}
		cursor = *page.NextCursor
	}
}

// itemIDs returns the task IDs of a section in order.
func itemIDs(items []MyTaskItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.TaskID)
	}
	return out
}

// setCopyStatus sets one member's copy of a task to a status, keeping the
// completion and decision fields consistent with it as the state CHECK requires.
func setCopyStatus(t *testing.T, f fixture, taskID, memberID, status string) {
	t.Helper()
	var completedAt, decidedAt any
	switch status {
	case "submitted":
		completedAt = dbTime(f.clk.now)
	case "approved", "rejected":
		completedAt = dbTime(f.clk.now)
		decidedAt = dbTime(f.clk.now)
	}
	execSQL(t, f.db,
		`UPDATE task_occurrence_assignees SET status = ?, completed_at = ?, decided_at = ? WHERE task_occurrence_id = ? AND family_member_id = ?`,
		status, completedAt, decidedAt, taskID, memberID)
}

func TestMyTasksSortsCopiesIntoSections(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)

	past := createTask(t, f, f.parent, oneOffBody("Past", "2026-10-10", child.memberID))
	setOccurrence(t, f, past.ID, utc(10, 8), nil)

	open := createTask(t, f, f.parent, oneOffBody("Open", "2026-10-10", child.memberID))
	setOccurrence(t, f, open.ID, utc(10, 12), nil)

	soon := createTask(t, f, f.parent, oneOffBody("Soon", "2026-10-11", child.memberID))
	setOccurrence(t, f, soon.ID, utc(11, 9), nil)

	far := createTask(t, f, f.parent, oneOffBody("Far", "2026-10-20", child.memberID))

	missed := createTask(t, f, f.parent, oneOffBody("Missed", "2026-10-10", child.memberID))
	expires := utc(10, 8).Add(30 * time.Minute)
	setOccurrence(t, f, missed.ID, utc(10, 8), &expires)

	waiting := newActionTask(t, f, true, child.memberID)
	setOccurrence(t, f, waiting.ID, utc(10, 13), nil)
	runAction(t, f, child, waiting.ID, "complete", nil, waiting.Version)

	done := newActionTask(t, f, false, child.memberID)
	setOccurrence(t, f, done.ID, utc(10, 14), nil)
	runAction(t, f, child, done.ID, "complete", nil, done.Version)

	olderDone := createTask(t, f, f.parent, oneOffBody("Older", "2026-10-09", child.memberID))
	setOccurrence(t, f, olderDone.ID, utc(9, 15), nil)
	setCopyStatus(t, f, olderDone.ID, child.memberID, "approved")

	resp := myTasksGet(t, f, child)

	if resp.Date != "2026-10-10" || resp.Timezone != "UTC" {
		t.Fatalf("date %q timezone %q, want 2026-10-10 UTC", resp.Date, resp.Timezone)
	}
	// Five copies are due today: past, open, missed, waiting and done. Waiting and done count as done.
	if resp.Summary.TodayTotal != 5 || resp.Summary.TodayDone != 2 {
		t.Fatalf("summary %+v, want 2 of 5 done", resp.Summary)
	}
	if resp.Summary.NextUp == nil || resp.Summary.NextUp.TaskID != open.ID {
		t.Fatalf("next_up = %+v, want the open task", resp.Summary.NextUp)
	}
	if got := itemIDs(resp.Today); !equalStrings(got, []string{open.ID}) {
		t.Fatalf("today = %v, want only the open task", got)
	}
	if got := itemIDs(resp.CatchUp); !equalStrings(got, []string{past.ID}) || !resp.CatchUp[0].PastDue {
		t.Fatalf("catch_up = %v, want the past task flagged past_due", got)
	}
	comingItems := myListGet(t, f, child, myComingPath).Tasks
	if got := itemIDs(comingItems); !equalStrings(got, []string{soon.ID}) {
		t.Fatalf("coming_up = %v, want the tomorrow task", got)
	}
	if got := itemIDs(resp.Waiting); !equalStrings(got, []string{waiting.ID}) {
		t.Fatalf("waiting = %v, want the submitted task", got)
	}
	doneItems := myListGet(t, f, child, myDonePath).Tasks
	if got := itemIDs(doneItems); !equalStrings(got, []string{done.ID, olderDone.ID}) {
		t.Fatalf("done = %v, want newest first", got)
	}

	// The far task appears in no list. The missed copy appears only in expired.
	for _, list := range [][]MyTaskItem{resp.Today, resp.CatchUp, resp.Waiting, resp.Expired, comingItems, doneItems} {
		for _, it := range list {
			if it.TaskID == far.ID {
				t.Fatalf("task %q should not be listed", it.Title)
			}
		}
	}
	if got := itemIDs(resp.Expired); !equalStrings(got, []string{missed.ID}) {
		t.Fatalf("expired = %v, want the missed copy", got)
	}
	for _, list := range [][]MyTaskItem{resp.Today, resp.CatchUp, resp.Waiting, comingItems, doneItems} {
		for _, it := range list {
			if it.TaskID == missed.ID {
				t.Fatalf("missed copy %q should only be in expired", it.Title)
			}
		}
	}
}

func TestMyTasksDoneCarriesApproverName(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)

	approved := newActionTask(t, f, true, child.memberID)
	setOccurrence(t, f, approved.ID, utc(10, 13), nil)
	submitted := runAction(t, f, child, approved.ID, "complete", nil, approved.Version)
	runAction(t, f, f.parent, approved.ID, "approve", memberBody(child.memberID), submitted.Version)

	auto := newActionTask(t, f, false, child.memberID)
	setOccurrence(t, f, auto.ID, utc(10, 14), nil)
	runAction(t, f, child, auto.ID, "complete", nil, auto.Version)

	var parentName string
	if err := f.db.QueryRow(`SELECT u.display_name FROM family_members m JOIN users u ON u.id = m.user_id WHERE m.id = ?`, f.parent.memberID).Scan(&parentName); err != nil {
		t.Fatalf("read parent name: %v", err)
	}

	byID := map[string]MyTaskItem{}
	for _, it := range myListGet(t, f, child, myDonePath).Tasks {
		byID[it.TaskID] = it
	}
	got := byID[approved.ID]
	if got.DecidedByName == nil || *got.DecidedByName != parentName || got.DecidedAt == nil {
		t.Errorf("approved item decided_by_name=%v decided_at=%v, want %q and a time", got.DecidedByName, got.DecidedAt, parentName)
	}
	auto1 := byID[auto.ID]
	if auto1.DecidedAt == nil || auto1.DecidedByName != nil {
		t.Errorf("automatic item decided_by_name=%v decided_at=%v, want no name and a time", auto1.DecidedByName, auto1.DecidedAt)
	}
}

func TestMyTasksPagesComingUpAndDone(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)

	// Five open copies due on days 11 to 15, and four approved copies due on days 5 to 8.
	var coming []string
	for i, day := range []string{"2026-10-11", "2026-10-12", "2026-10-13", "2026-10-14", "2026-10-15"} {
		task := createTask(t, f, f.parent, oneOffBody("Coming "+day, day, child.memberID))
		setOccurrence(t, f, task.ID, utc(11+i, 9), nil)
		coming = append(coming, task.ID)
	}
	var done []string
	for i, day := range []string{"2026-10-05", "2026-10-06", "2026-10-07", "2026-10-08"} {
		task := createTask(t, f, f.parent, oneOffBody("Done "+day, day, child.memberID))
		setOccurrence(t, f, task.ID, utc(5+i, 15), nil)
		setCopyStatus(t, f, task.ID, child.memberID, "approved")
		done = append(done, task.ID)
	}
	var wantDone []string
	for i := len(done) - 1; i >= 0; i-- {
		wantDone = append(wantDone, done[i])
	}

	gotComing, pages := walkPages(t, f, child, myComingPath, "2")
	if !equalStrings(gotComing, coming) || pages != 3 {
		t.Fatalf("coming over %d pages = %v, want %v over 3 pages", pages, gotComing, coming)
	}
	gotDone, donePages := walkPages(t, f, child, myDonePath, "2")
	if !equalStrings(gotDone, wantDone) || donePages != 2 {
		t.Fatalf("done over %d pages = %v, want %v over 2 pages", donePages, gotDone, wantDone)
	}

	if page := myListGet(t, f, child, myComingPath); len(page.Tasks) != 5 || page.NextCursor != nil {
		t.Fatalf("default page has %d tasks, next_cursor %v; want all 5 and no cursor", len(page.Tasks), page.NextCursor)
	}

	for _, path := range []string{myComingPath + "?limit=0", myDonePath + "?limit=201"} {
		expectValidation(t, f.do(t, child, http.MethodGet, path, nil, nil), "limit", "invalid")
	}
	expectValidation(t, f.do(t, child, http.MethodGet, myDonePath+"?cursor=bogus", nil, nil), "cursor", "invalid")
}

func TestMyTasksMainPayloadOmitsPagedLists(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	soon := createTask(t, f, f.parent, oneOffBody("Soon", "2026-10-11", child.memberID))
	setOccurrence(t, f, soon.ID, utc(11, 9), nil)

	rec := f.do(t, child, http.MethodGet, "/api/v1/me/tasks", nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode my tasks: %v", err)
	}
	for _, key := range []string{"coming_up", "done"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("/me/tasks still sends %q; the paged endpoints own it", key)
		}
	}
}

func TestMyTasksShowsOnlyTheCallersCopies(t *testing.T) {
	f := newFixture(t)
	ava := addChildMember(t, f, f.a.FamilyID)
	ben := addChildMember(t, f, f.a.FamilyID)

	mine := createTask(t, f, f.parent, oneOffBody("Mine", "2026-10-10", ava.memberID))
	createTask(t, f, f.parent, oneOffBody("Theirs", "2026-10-10", ben.memberID))

	resp := myTasksGet(t, f, ava)
	if got := itemIDs(resp.Today); !equalStrings(got, []string{mine.ID}) {
		t.Fatalf("today = %v, want only the caller's copy", got)
	}
	if resp.Summary.TodayTotal != 1 {
		t.Fatalf("today_total = %d, want 1", resp.Summary.TodayTotal)
	}
}

func TestMyTasksHidesSkippedCopies(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)

	skipped := createTask(t, f, f.parent, oneOffBody("Skipped", "2026-10-10", child.memberID))
	setCopyStatus(t, f, skipped.ID, child.memberID, "skipped")

	resp := myTasksGet(t, f, child)
	if len(resp.Today) != 0 || resp.Summary.TodayTotal != 0 {
		t.Fatalf("today %v total %d, want a skipped copy hidden", itemIDs(resp.Today), resp.Summary.TodayTotal)
	}
}

func TestMyTasksRequiresMembership(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, actor{}, http.MethodGet, "/api/v1/me/tasks", nil, nil)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 401 or 403 without a member", rec.Code)
	}
}

func TestMyTasksStreakCountsCompleteDays(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)

	// 2026-10-07 is incomplete and ends the run. 2026-10-08 and 2026-10-09 are complete.
	old := createTask(t, f, f.parent, oneOffBody("Old", "2026-10-07", child.memberID))
	setCopyStatus(t, f, old.ID, child.memberID, "to_do")
	for _, day := range []string{"2026-10-08", "2026-10-09"} {
		done := createTask(t, f, f.parent, oneOffBody("Done "+day, day, child.memberID))
		setCopyStatus(t, f, done.ID, child.memberID, "approved")
	}
	today := createTask(t, f, f.parent, oneOffBody("Today", "2026-10-10", child.memberID))

	if got := myTasksGet(t, f, child).StreakDays; got != 2 {
		t.Fatalf("streak with today open = %d, want 2", got)
	}

	setCopyStatus(t, f, today.ID, child.memberID, "approved")
	if got := myTasksGet(t, f, child).StreakDays; got != 3 {
		t.Fatalf("streak with today done = %d, want 3", got)
	}
}

func TestMyTasksUsesFamilyZoneAcrossDST(t *testing.T) {
	f := newFixture(t)
	execSQL(t, f.db, `UPDATE families SET timezone = 'Europe/Berlin' WHERE id = ?`, f.a.FamilyID)
	// 23:30 UTC on 28 March is 00:30 on 29 March in Berlin, the day after the family's last CET day.
	f.clk.now = time.Date(2026, 3, 28, 23, 30, 0, 0, time.UTC)
	child := addChildMember(t, f, f.a.FamilyID)

	berlin := createTask(t, f, f.parent, oneOffBody("Berlin", "2026-03-29", child.memberID))

	resp := myTasksGet(t, f, child)
	if resp.Date != "2026-03-29" || resp.Timezone != "Europe/Berlin" {
		t.Fatalf("date %q zone %q, want 2026-03-29 Europe/Berlin", resp.Date, resp.Timezone)
	}
	if got := itemIDs(resp.Today); !equalStrings(got, []string{berlin.ID}) {
		t.Fatalf("today = %v, want the Berlin copy", got)
	}
}

func TestBuildMyTasksMarksPastDueAndMissed(t *testing.T) {
	now := utc(10, 9)
	expired := utc(10, 8)
	rows := []myRow{
		{taskID: "late", dueDate: "2026-10-10", dueAt: utc(10, 8), status: "to_do", occurrenceStatus: "past_due"},
		{taskID: "gone", dueDate: "2026-10-10", dueAt: utc(10, 7), expiresAt: &expired, status: "to_do", occurrenceStatus: "scheduled"},
		{taskID: "flagged", dueDate: "2026-10-10", dueAt: utc(10, 11), status: "to_do", occurrenceStatus: "missed"},
	}
	resp := buildMyTasks(rows, now, "2026-10-10")

	if got := itemIDs(resp.CatchUp); !equalStrings(got, []string{"late"}) {
		t.Fatalf("catch_up = %v, want only late", got)
	}
	if got := itemIDs(resp.Today); len(got) != 0 {
		t.Fatalf("today = %v, want empty; missed copies are not today's open work", got)
	}
	if got := itemIDs(resp.Expired); !equalStrings(got, []string{"flagged", "gone"}) {
		t.Fatalf("expired = %v, want the expired and missed copies, newest first", got)
	}
	if resp.Summary.TodayTotal != 3 || resp.Summary.TodayDone != 0 {
		t.Fatalf("summary %+v, want 0 of 3", resp.Summary)
	}
}

func TestMyTaskItemPenaltyPoints(t *testing.T) {
	percent := myRow{
		taskID:       "p",
		points:       15,
		penaltyValue: sql.NullInt64{Int64: 33, Valid: true},
		penaltyUnit:  sql.NullString{String: unitPercent, Valid: true},
	}
	it := percent.item()
	if it.PenaltyPoints == nil || *it.PenaltyPoints != 5 {
		t.Fatalf("percent penalty points = %v, want 5 (33%% of 15)", it.PenaltyPoints)
	}

	fixed := myRow{
		taskID:       "f",
		points:       15,
		penaltyValue: sql.NullInt64{Int64: 3, Valid: true},
		penaltyUnit:  sql.NullString{String: unitPoints, Valid: true},
	}
	it = fixed.item()
	if it.PenaltyPoints == nil || *it.PenaltyPoints != 3 || it.PenaltyUnit == nil || *it.PenaltyUnit != unitPoints {
		t.Fatalf("points penalty = %+v, want 3 points", it)
	}

	none := myRow{taskID: "n", points: 15}
	if it := none.item(); it.PenaltyPoints != nil || it.PenaltyValue != nil {
		t.Fatalf("no penalty should leave the fields nil, got %+v", it)
	}
}

func TestStreakDays(t *testing.T) {
	const today = "2026-10-10"
	cases := []struct {
		name string
		days []dayTotals
		want int
	}{
		{"no days", nil, 0},
		{"today open is neutral", []dayTotals{{today, 1, 0}}, 0},
		{"today done counts", []dayTotals{{today, 1, 1}}, 1},
		{"open today then two complete days", []dayTotals{{today, 1, 0}, {"2026-10-09", 1, 1}, {"2026-10-08", 1, 1}}, 2},
		{"incomplete yesterday ends the run", []dayTotals{{today, 1, 1}, {"2026-10-09", 1, 0}, {"2026-10-08", 1, 1}}, 1},
		{"a day with no copies is neutral", []dayTotals{{today, 1, 1}, {"2026-10-09", 0, 0}, {"2026-10-08", 1, 1}}, 2},
		{"two incomplete days", []dayTotals{{today, 1, 0}, {"2026-10-09", 1, 0}}, 0},
		{"part done today is neutral", []dayTotals{{today, 2, 1}, {"2026-10-09", 1, 1}}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := streakDays(tc.days, today); got != tc.want {
				t.Fatalf("streakDays = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAddDaysISOAcrossDST(t *testing.T) {
	cases := map[string]struct {
		date string
		days int
		want string
	}{
		"into the spring change": {"2026-03-28", 1, "2026-03-29"},
		"past the spring change": {"2026-03-29", 1, "2026-03-30"},
		"back a month":           {"2026-10-10", -30, "2026-09-10"},
	}
	for name, tc := range cases {
		if got := addDaysISO(tc.date, tc.days); got != tc.want {
			t.Fatalf("%s: addDaysISO(%s, %d) = %s, want %s", name, tc.date, tc.days, got, tc.want)
		}
	}
}

// equalStrings reports whether two slices hold the same values in the same order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
