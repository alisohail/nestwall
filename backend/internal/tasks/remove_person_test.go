package tasks

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

// removePath is the remove-person route for a task.
func removePath(taskID string) string { return tasksPath + "/" + taskID + "/remove-person" }

// removeBody is the request body that takes one person off a task.
func removeBody(memberID, scope string) map[string]any {
	return map[string]any{"member_id": memberID, "scope": scope}
}

// createRepeat creates a repeating task with a daily rule and returns the create response.
func createRepeat(t *testing.T, f fixture, a actor, title, startsOn string, members ...string) repeatCreated {
	t.Helper()
	body := oneOffBody(title, startsOn, members...)
	body["recurrence_rule"] = "FREQ=DAILY;COUNT=5"
	rec := f.do(t, a, http.MethodPost, definitionsPath, body, nil)
	expectStatus(t, rec, http.StatusCreated)
	var got repeatCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode repeat %q: %v", rec.Body.String(), err)
	}
	if got.FirstTask == nil {
		t.Fatalf("repeat has no first task: %s", rec.Body.String())
	}
	return got
}

// definitionOf returns the definition an occurrence belongs to.
func definitionOf(t *testing.T, f fixture, occurrenceID string) string {
	t.Helper()
	var id string
	if err := f.db.QueryRow(
		`SELECT task_definition_id FROM task_occurrences WHERE id = ?`, occurrenceID,
	).Scan(&id); err != nil {
		t.Fatalf("find definition of %s: %v", occurrenceID, err)
	}
	return id
}

// occurrenceOn returns the ID of a definition's occurrence due on a date.
func occurrenceOn(t *testing.T, f fixture, definitionID, date string) string {
	t.Helper()
	var id string
	if err := f.db.QueryRow(
		`SELECT id FROM task_occurrences WHERE task_definition_id = ? AND due_date = ?`, definitionID, date,
	).Scan(&id); err != nil {
		t.Fatalf("find occurrence on %s: %v", date, err)
	}
	return id
}

// copyDates lists the due dates of one person's copies of a definition, in date order.
func copyDates(t *testing.T, f fixture, definitionID, memberID string) []string {
	t.Helper()
	rows, err := f.db.Query(
		`SELECT o.due_date FROM task_occurrence_assignees oa
		 JOIN task_occurrences o ON o.id = oa.task_occurrence_id
		 WHERE o.task_definition_id = ? AND oa.family_member_id = ? ORDER BY o.due_date`,
		definitionID, memberID)
	if err != nil {
		t.Fatalf("list copies: %v", err)
	}
	defer rows.Close()
	var dates []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatalf("scan copy: %v", err)
		}
		dates = append(dates, d)
	}
	return dates
}

// definitionHasAssignee reports whether a person is still assigned to the definition.
func definitionHasAssignee(t *testing.T, f fixture, definitionID, memberID string) bool {
	t.Helper()
	var n int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM task_definition_assignees WHERE task_definition_id = ? AND family_member_id = ?`,
		definitionID, memberID,
	).Scan(&n); err != nil {
		t.Fatalf("count definition assignees: %v", err)
	}
	return n > 0
}

func TestRemovePersonOneOffKeepsOtherPerson(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID, child.memberID))

	rec := f.do(t, f.parent, http.MethodPost, removePath(created.ID), removeBody(child.memberID, "occurrence"), ifMatch(1))
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if got.Archived || got.Version != 2 || rec.Header().Get("ETag") != `"2"` {
		t.Fatalf("archived=%v version=%d ETag=%q; want false, 2, \"2\"", got.Archived, got.Version, rec.Header().Get("ETag"))
	}
	if len(got.Assignees) != 1 || got.Assignees[0].MemberID != f.parent.memberID {
		t.Fatalf("assignees = %+v, want only the parent", got.Assignees)
	}
	if definitionHasAssignee(t, f, definitionOf(t, f, created.ID), child.memberID) {
		t.Fatal("child is still assigned to the one-off task")
	}
	if n := countAudit(t, f, "task.person_removed", created.ID); n != 1 {
		t.Fatalf("audit task.person_removed rows = %d, want 1", n)
	}
}

func TestRemovePersonLastPersonArchivesTask(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))

	rec := f.do(t, f.parent, http.MethodPost, removePath(created.ID), removeBody(f.parent.memberID, "occurrence"), ifMatch(1))
	expectStatus(t, rec, http.StatusOK)
	if got := decodeTask(t, rec.Body.Bytes()); !got.Archived {
		t.Fatal("removing the last person did not archive the task")
	}
	if n := countAudit(t, f, "task.archived", created.ID); n != 1 {
		t.Fatalf("audit task.archived rows = %d, want 1", n)
	}
	if n := countAudit(t, f, "task.person_removed", created.ID); n != 0 {
		t.Fatalf("audit task.person_removed rows = %d, want 0 when the task is archived", n)
	}
}

func TestRemovePersonRemovesCopyAlreadyActedOn(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID, child.memberID))
	execSQL(t, f.db,
		`UPDATE task_occurrence_assignees SET status = 'skipped' WHERE task_occurrence_id = ? AND family_member_id = ?`,
		created.ID, child.memberID)

	rec := f.do(t, f.parent, http.MethodPost, removePath(created.ID),
		removeBody(child.memberID, "occurrence"), ifMatch(1))
	expectStatus(t, rec, http.StatusOK)
	if got := copyDates(t, f, definitionOf(t, f, created.ID), child.memberID); len(got) != 0 {
		t.Fatalf("child's acted-on copy was kept: %v", got)
	}
	if got := decodeTask(t, rec.Body.Bytes()); got.Archived || len(got.Assignees) != 1 {
		t.Fatalf("archived=%v assignees=%+v, want the task kept with only the parent", got.Archived, got.Assignees)
	}
}

func TestRemovePersonSeriesKeepsPastAndDropsUpcomingCopies(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	rep := createRepeat(t, f, f.parent, "Walk dog", "2026-10-10", f.parent.memberID, child.memberID)
	// Move the clock to the 12th so the 10th and 11th copies are in the past.
	f.clk.now = fixtureNow.AddDate(0, 0, 2)
	defID := rep.DefinitionID

	rec := f.do(t, f.parent, http.MethodPost, removePath(rep.FirstTask.ID),
		removeBody(child.memberID, "series"), ifMatch(rep.FirstTask.Version))
	expectStatus(t, rec, http.StatusOK)

	if definitionHasAssignee(t, f, defID, child.memberID) {
		t.Fatal("child is still assigned to the series")
	}
	if got := copyDates(t, f, defID, child.memberID); !slices.Equal(got, []string{"2026-10-10", "2026-10-11"}) {
		t.Fatalf("child copies = %v, want the two past dates only", got)
	}
	if got := copyDates(t, f, defID, f.parent.memberID); len(got) != 5 {
		t.Fatalf("parent copies = %v, want all 5 kept", got)
	}
}

func TestRemovePersonOneDayKeepsSeries(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	rep := createRepeat(t, f, f.parent, "Walk dog", "2026-10-10", f.parent.memberID, child.memberID)
	target := occurrenceOn(t, f, rep.DefinitionID, "2026-10-11")

	rec := f.do(t, f.parent, http.MethodPost, removePath(target),
		removeBody(child.memberID, "occurrence"), ifMatch(rep.FirstTask.Version))
	expectStatus(t, rec, http.StatusOK)

	if !definitionHasAssignee(t, f, rep.DefinitionID, child.memberID) {
		t.Fatal("removing one day took the child off the series")
	}
	want := []string{"2026-10-10", "2026-10-12", "2026-10-13", "2026-10-14"}
	if got := copyDates(t, f, rep.DefinitionID, child.memberID); !slices.Equal(got, want) {
		t.Fatalf("child copies = %v, want %v", got, want)
	}
}

func TestRemovePersonSeriesRemovesActedOnUpcomingCopy(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	rep := createRepeat(t, f, f.parent, "Walk dog", "2026-10-10", f.parent.memberID, child.memberID)
	execSQL(t, f.db,
		`UPDATE task_occurrence_assignees SET status = 'skipped'
		 WHERE family_member_id = ? AND task_occurrence_id = ?`,
		child.memberID, occurrenceOn(t, f, rep.DefinitionID, "2026-10-12"))

	expectStatus(t, f.do(t, f.parent, http.MethodPost, removePath(rep.FirstTask.ID),
		removeBody(child.memberID, "series"), ifMatch(rep.FirstTask.Version)), http.StatusOK)
	if definitionHasAssignee(t, f, rep.DefinitionID, child.memberID) {
		t.Fatal("child is still assigned to the series")
	}
	if got := copyDates(t, f, rep.DefinitionID, child.memberID); len(got) != 0 {
		t.Fatalf("child copies = %v, want none (every copy is today or later)", got)
	}
	if got := copyDates(t, f, rep.DefinitionID, f.parent.memberID); len(got) != 5 {
		t.Fatalf("parent copies = %v, want all 5 kept", got)
	}
}

func TestRemovePersonValidationAndAccess(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID, child.memberID))
	path := removePath(created.ID)

	expectError(t, f.do(t, f.parent, http.MethodPost, path,
		removeBody(child.memberID, "everyone"), ifMatch(1)), http.StatusBadRequest, "validation_failed")
	expectError(t, f.do(t, f.parent, http.MethodPost, path,
		removeBody(child.memberID, "series"), ifMatch(1)), http.StatusBadRequest, "validation_failed")
	expectStatus(t, f.do(t, f.parent, http.MethodPost, path,
		removeBody(f.other.memberID, "occurrence"), ifMatch(1)), http.StatusNotFound)
	expectStatus(t, f.do(t, f.parent, http.MethodPost, path,
		removeBody(child.memberID, "occurrence"), ifMatch(9)), http.StatusPreconditionFailed)
	expectStatus(t, f.do(t, f.other, http.MethodPost, path,
		removeBody(child.memberID, "occurrence"), ifMatch(1)), http.StatusNotFound)
}
