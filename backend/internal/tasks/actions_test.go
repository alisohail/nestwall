package tasks

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/platform/ids"
)

// testClock is the clock the tests set. Handlers read it through clock.Clock, so Now has a
// pointer receiver.
type testClock struct{ now time.Time }

// Now returns the time the test set.
func (c *testClock) Now() time.Time { return c.now }

// copyRow is one person's stored copy of a one-off task.
type copyRow struct {
	status      string
	completedAt *string
	decidedAt   *string
	decidedBy   *string
	note        *string
	late        int
}

// actionPath returns the path of an action on a task.
func actionPath(taskID, name string) string { return tasksPath + "/" + taskID + "/" + name }

// checklistPath returns the path of one checklist item on a task.
func checklistPath(taskID, itemID string) string {
	return tasksPath + "/" + taskID + "/checklist/" + itemID
}

// ifMatch returns the If-Match header for a task version.
func ifMatch(version int) map[string]string {
	return map[string]string{"If-Match": fmt.Sprintf(`"%d"`, version)}
}

// memberBody is the body of an action that names the member whose copy it acts on.
func memberBody(memberID string) map[string]any {
	return map[string]any{"member_id": memberID}
}

// newActionTask creates a one-off task due at the end of 2026-10-10 for the given assignees. At
// fixtureNow it is neither late nor missed.
func newActionTask(t *testing.T, f fixture, approval bool, assignees ...string) task {
	t.Helper()
	body := oneOffBody("Tidy room", "2026-10-10", assignees...)
	body["approval_required"] = approval
	return createTask(t, f, f.parent, body)
}

// setOccurrence sets when the task's occurrence is due and when it expires. A nil expiry means
// the task never expires.
func setOccurrence(t *testing.T, f fixture, taskID string, due time.Time, expires *time.Time) {
	t.Helper()
	var expiry any
	if expires != nil {
		expiry = dbTime(*expires)
	}
	execSQL(t, f.db,
		`UPDATE task_occurrences SET due_at = ?, expires_at = ? WHERE id = ?`,
		dbTime(due), expiry, taskID)
}

// addChecklistItem adds an item to the task's occurrence and returns its ID.
func addChecklistItem(t *testing.T, f fixture, taskID, text string, sortOrder int) string {
	t.Helper()
	itemID := ids.New()
	execSQL(t, f.db,
		`INSERT INTO task_checklist_items (id, family_id, task_occurrence_id, sort_order, item_text)
		 SELECT ?, ?, o.id, ?, ? FROM task_occurrences o WHERE o.id = ?`,
		itemID, f.a.FamilyID, sortOrder, text, taskID)
	return itemID
}

// readCopy reads the stored copy of one member for a one-off task.
func readCopy(t *testing.T, f fixture, taskID, memberID string) copyRow {
	t.Helper()
	var c copyRow
	if err := f.db.QueryRow(
		`SELECT oa.status, CAST(oa.completed_at AS TEXT), CAST(oa.decided_at AS TEXT),
			oa.decided_by_member_id, oa.decision_note, oa.completed_late
		 FROM task_occurrence_assignees oa
		 JOIN task_occurrences o ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id
		 WHERE o.id = ? AND oa.family_member_id = ?`,
		taskID, memberID,
	).Scan(&c.status, &c.completedAt, &c.decidedAt, &c.decidedBy, &c.note, &c.late); err != nil {
		t.Fatalf("read copy: %v", err)
	}
	return c
}

// recordEvents subscribes to the handler's events and returns the list they are appended to.
// Submitted and rejected events only drive notices; notify_test.go covers them, so they are left out here.
func recordEvents(f fixture) *[]Event {
	got := &[]Event{}
	f.h.Subscribe(func(_ context.Context, e Event) {
		if e.Type == EventSubmitted || e.Type == EventRejected {
			return
		}
		*got = append(*got, e)
	})
	return got
}

// runAction posts an action and fails the test unless it returns 200. It returns the task as the
// response shows it.
func runAction(t *testing.T, f fixture, a actor, taskID, name string, body map[string]any, version int) task {
	t.Helper()
	var payload any
	if body != nil {
		payload = body
	}
	rec := f.do(t, a, http.MethodPost, actionPath(taskID, name), payload, ifMatch(version))
	expectStatus(t, rec, http.StatusOK)
	return decodeTask(t, rec.Body.Bytes())
}

// tickItem sets one checklist item and fails the test unless the request returns 200.
func tickItem(t *testing.T, f fixture, a actor, taskID, itemID string, done bool, version int) task {
	t.Helper()
	rec := f.do(t, a, http.MethodPut, checklistPath(taskID, itemID), map[string]any{"done": done}, ifMatch(version))
	expectStatus(t, rec, http.StatusOK)
	return decodeTask(t, rec.Body.Bytes())
}

func TestNextStatus(t *testing.T) {
	cases := []struct {
		from     string
		act      action
		approval bool
		to       string
		ok       bool
	}{
		{taskStatusToDo, actionComplete, true, statusSubmitted, true},
		{taskStatusToDo, actionComplete, false, statusApproved, true},
		{statusRejected, actionComplete, true, statusSubmitted, true},
		{statusRejected, actionComplete, false, statusApproved, true},
		{statusSubmitted, actionComplete, true, "", false},
		{statusApproved, actionComplete, false, "", false},
		{statusSkipped, actionComplete, true, "", false},
		{statusSubmitted, actionApprove, true, statusApproved, true},
		{taskStatusToDo, actionApprove, true, "", false},
		{statusApproved, actionApprove, true, "", false},
		{statusRejected, actionApprove, true, "", false},
		{statusSkipped, actionApprove, true, "", false},
		{statusSubmitted, actionReject, true, statusRejected, true},
		{taskStatusToDo, actionReject, true, "", false},
		{statusApproved, actionReject, true, "", false},
		{taskStatusToDo, actionSkip, true, statusSkipped, true},
		{statusSubmitted, actionSkip, true, "", false},
		{statusApproved, actionSkip, true, "", false},
		{statusRejected, actionSkip, true, "", false},
		{statusSkipped, actionSkip, true, "", false},
		{statusApproved, actionReopen, true, taskStatusToDo, true},
		{statusSkipped, actionReopen, true, taskStatusToDo, true},
		{statusRejected, actionReopen, true, taskStatusToDo, true},
		{taskStatusToDo, actionReopen, true, "", false},
		{statusSubmitted, actionReopen, true, "", false},
		{statusSubmitted, actionWithdraw, true, taskStatusToDo, true},
		{taskStatusToDo, actionWithdraw, true, "", false},
		{statusRejected, actionWithdraw, true, "", false},
		{statusApproved, actionWithdraw, true, "", false},
	}
	for _, c := range cases {
		to, ok := nextStatus(c.from, c.act, c.approval)
		if to != c.to || ok != c.ok {
			t.Errorf("nextStatus(%s, %s, %v) = (%q, %v), want (%q, %v)",
				c.from, c.act, c.approval, to, ok, c.to, c.ok)
		}
	}
}

func TestCompleteWithoutApprovalIsApproved(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)
	events := recordEvents(f)

	updated := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)
	if updated.Version != tk.Version+1 {
		t.Errorf("version = %d, want %d", updated.Version, tk.Version+1)
	}
	c := readCopy(t, f, tk.ID, child.memberID)
	if c.status != statusApproved || c.completedAt == nil || c.decidedAt == nil {
		t.Errorf("copy = %+v, want approved with completed_at and decided_at set", c)
	}
	if c.decidedBy != nil {
		t.Errorf("decided_by = %q, want NULL for an automatic approval", *c.decidedBy)
	}
	if len(*events) != 1 || (*events)[0].Type != EventApproved || (*events)[0].ActorID != "" || (*events)[0].Late {
		t.Errorf("events = %+v, want one automatic approved event", *events)
	}
	if n := countAudit(t, f, "task.completed", tk.ID); n != 1 {
		t.Errorf("audit task.completed rows = %d, want 1", n)
	}
}

func TestCompleteNeedsApprovalThenApprove(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	events := recordEvents(f)

	submitted := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)
	c := readCopy(t, f, tk.ID, child.memberID)
	if c.status != statusSubmitted || c.decidedAt != nil {
		t.Errorf("copy = %+v, want submitted with no decision", c)
	}
	if len(*events) != 0 {
		t.Errorf("events after submit = %+v, want none", *events)
	}

	runAction(t, f, f.parent, tk.ID, "approve", memberBody(child.memberID), submitted.Version)
	c = readCopy(t, f, tk.ID, child.memberID)
	if c.status != statusApproved || c.completedAt == nil || c.decidedAt == nil {
		t.Errorf("copy = %+v, want approved with completed_at and decided_at set", c)
	}
	if c.decidedBy == nil || *c.decidedBy != f.parent.memberID {
		t.Errorf("decided_by = %v, want the parent", c.decidedBy)
	}
	if len(*events) != 1 || (*events)[0].Type != EventApproved ||
		(*events)[0].ActorID != f.parent.memberID || (*events)[0].MemberID != child.memberID {
		t.Errorf("events = %+v, want one approved event from the parent", *events)
	}
	if n := countAudit(t, f, "task.approved", tk.ID); n != 1 {
		t.Errorf("audit task.approved rows = %d, want 1", n)
	}
}

func TestRejectRequiresNoteThenRecomplete(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	submitted := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)
	events := recordEvents(f)

	cases := []struct {
		note   any
		reason string
	}{
		{nil, "required"},
		{"   ", "required"},
		{strings.Repeat("x", 501), "too_long"},
	}
	for _, c := range cases {
		rec := f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "reject"),
			map[string]any{"member_id": child.memberID, "note": c.note}, ifMatch(submitted.Version))
		params := expectError(t, rec, http.StatusBadRequest, "validation_failed")
		if params["field"] != "note" || params["reason"] != c.reason {
			t.Errorf("params = %v, want field note and reason %s", params, c.reason)
		}
	}

	rejected := runAction(t, f, f.parent, tk.ID, "reject",
		map[string]any{"member_id": child.memberID, "note": "Make the bed first"}, submitted.Version)
	c := readCopy(t, f, tk.ID, child.memberID)
	if c.status != statusRejected || c.note == nil || *c.note != "Make the bed first" ||
		c.decidedBy == nil || *c.decidedBy != f.parent.memberID || c.completedAt == nil {
		t.Errorf("copy = %+v, want rejected with the note, the parent as decider and completed_at kept", c)
	}

	runAction(t, f, child, tk.ID, "complete", nil, rejected.Version)
	c = readCopy(t, f, tk.ID, child.memberID)
	if c.status != statusSubmitted || c.note != nil || c.decidedAt != nil {
		t.Errorf("copy = %+v, want submitted with the old decision cleared", c)
	}
	if len(*events) != 0 {
		t.Errorf("events = %+v, want none while approval is required", *events)
	}
}

func TestWithdrawSubmittedCopy(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	submitted := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)
	events := recordEvents(f)

	withdrawn := runAction(t, f, child, tk.ID, "withdraw", nil, submitted.Version)
	c := readCopy(t, f, tk.ID, child.memberID)
	if c.status != taskStatusToDo || c.completedAt != nil || c.decidedAt != nil {
		t.Errorf("copy = %+v, want to_do with no completion or decision", c)
	}
	if len(*events) != 0 {
		t.Errorf("events = %+v, want none: nothing was posted at submission", *events)
	}
	if n := countAudit(t, f, "task.withdrawn", tk.ID); n != 1 {
		t.Errorf("audit task.withdrawn rows = %d, want 1", n)
	}

	rec := f.do(t, child, http.MethodPost, actionPath(tk.ID, "withdraw"), nil, ifMatch(withdrawn.Version))
	expectError(t, rec, http.StatusConflict, "invalid_transition")

	runAction(t, f, child, tk.ID, "complete", nil, withdrawn.Version)
	if c = readCopy(t, f, tk.ID, child.memberID); c.status != statusSubmitted {
		t.Errorf("status after submitting again = %s, want submitted", c.status)
	}
}

func TestSkipAndReopen(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	events := recordEvents(f)

	rec := f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "skip"),
		map[string]any{"member_id": child.memberID, "note": "Away"}, ifMatch(tk.Version))
	params := expectError(t, rec, http.StatusBadRequest, "validation_failed")
	if params["field"] != "note" || params["reason"] != "not_allowed" {
		t.Errorf("params = %v, want field note and reason not_allowed", params)
	}

	skipped := runAction(t, f, f.parent, tk.ID, "skip", memberBody(child.memberID), tk.Version)
	c := readCopy(t, f, tk.ID, child.memberID)
	if c.status != statusSkipped || c.completedAt != nil || c.decidedAt != nil {
		t.Errorf("copy = %+v, want skipped with no completion or decision", c)
	}

	reopened := runAction(t, f, f.parent, tk.ID, "reopen", memberBody(child.memberID), skipped.Version)
	if c = readCopy(t, f, tk.ID, child.memberID); c.status != taskStatusToDo {
		t.Errorf("status after reopen = %s, want to_do", c.status)
	}

	rec = f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "reopen"),
		memberBody(child.memberID), ifMatch(reopened.Version))
	expectError(t, rec, http.StatusConflict, "invalid_transition")

	submitted := runAction(t, f, child, tk.ID, "complete", nil, reopened.Version)
	approved := runAction(t, f, f.parent, tk.ID, "approve", memberBody(child.memberID), submitted.Version)
	runAction(t, f, f.parent, tk.ID, "reopen", memberBody(child.memberID), approved.Version)
	if c = readCopy(t, f, tk.ID, child.memberID); c.status != taskStatusToDo {
		t.Errorf("status after reopening an approved copy = %s, want to_do", c.status)
	}

	if len(*events) != 2 {
		t.Fatalf("events = %+v, want approved then reopened", *events)
	}
	last := (*events)[1]
	if last.Type != EventReopened || last.From != statusApproved || last.Missed || last.ActorID != f.parent.memberID {
		t.Errorf("reopen event = %+v, want reopened from approved by the parent, not missed", last)
	}
	if n := countAudit(t, f, "task.reopened", tk.ID); n != 2 {
		t.Errorf("audit task.reopened rows = %d, want 2", n)
	}
}

func TestInvalidTransitions(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)
	done := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)

	rec := f.do(t, child, http.MethodPost, actionPath(tk.ID, "complete"), nil, ifMatch(done.Version))
	params := expectError(t, rec, http.StatusConflict, "invalid_transition")
	if params["status"] != statusApproved || params["action"] != "complete" {
		t.Errorf("params = %v, want status approved and action complete", params)
	}

	rec = f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "approve"),
		memberBody(child.memberID), ifMatch(done.Version))
	expectError(t, rec, http.StatusConflict, "invalid_transition")

	rec = f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "skip"),
		memberBody(child.memberID), ifMatch(done.Version))
	expectError(t, rec, http.StatusConflict, "invalid_transition")
}

func TestCompleteRefusesOwnCopyNotYetDue(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)
	execSQL(t, f.db, `UPDATE task_occurrences SET due_date = '2026-10-12' WHERE id = ?`, tk.ID)

	rec := f.do(t, child, http.MethodPost, actionPath(tk.ID, "complete"), nil, ifMatch(tk.Version))
	expectError(t, rec, http.StatusConflict, "task_not_due")

	if c := readCopy(t, f, tk.ID, child.memberID); c.status != taskStatusToDo {
		t.Errorf("status = %s, want the copy left at to_do", c.status)
	}
}

func TestChildCannotTickNotYetDueCopy(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)
	item := addChecklistItem(t, f, tk.ID, "Bed made", 0)
	execSQL(t, f.db, `UPDATE task_occurrences SET due_date = '2026-10-12' WHERE id = ?`, tk.ID)

	rec := f.do(t, child, http.MethodPut, checklistPath(tk.ID, item),
		map[string]any{"done": true}, ifMatch(tk.Version))
	expectError(t, rec, http.StatusConflict, "task_not_due")
}

func TestParentCompletesOwnNotYetDueCopy(t *testing.T) {
	f := newFixture(t)
	tk := newActionTask(t, f, false, f.parent.memberID)
	execSQL(t, f.db, `UPDATE task_occurrences SET due_date = '2026-10-12' WHERE id = ?`, tk.ID)

	runAction(t, f, f.parent, tk.ID, "complete", nil, tk.Version)

	if c := readCopy(t, f, tk.ID, f.parent.memberID); c.status != statusApproved {
		t.Errorf("parent's copy = %+v, want approved", c)
	}
}

func TestMissedOccurrenceRefusesCompleteSkipAndTick(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	item := addChecklistItem(t, f, tk.ID, "Bed made", 0)
	expires := time.Date(2026, 10, 10, 8, 30, 0, 0, time.UTC)
	setOccurrence(t, f, tk.ID, time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC), &expires)

	rec := f.do(t, child, http.MethodPost, actionPath(tk.ID, "complete"), nil, ifMatch(tk.Version))
	expectError(t, rec, http.StatusConflict, "task_missed")

	rec = f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "skip"),
		memberBody(child.memberID), ifMatch(tk.Version))
	expectError(t, rec, http.StatusConflict, "task_missed")

	rec = f.do(t, child, http.MethodPut, checklistPath(tk.ID, item),
		map[string]any{"done": true}, ifMatch(tk.Version))
	expectError(t, rec, http.StatusConflict, "task_missed")

	if c := readCopy(t, f, tk.ID, child.memberID); c.status != taskStatusToDo {
		t.Errorf("status = %s, want the copy left at to_do", c.status)
	}
}

func TestLateCompletionIsRecorded(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)
	setOccurrence(t, f, tk.ID, time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC), nil)
	events := recordEvents(f)

	runAction(t, f, child, tk.ID, "complete", nil, tk.Version)
	c := readCopy(t, f, tk.ID, child.memberID)
	if c.status != statusApproved || c.late != 1 {
		t.Errorf("copy = %+v, want approved and completed_late set", c)
	}
	if len(*events) != 1 || !(*events)[0].Late {
		t.Errorf("events = %+v, want one approved event marked late", *events)
	}
}

func TestChecklistGatesCompletion(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, false, child.memberID)
	execSQL(t, f.db, `UPDATE task_definitions SET checklist_all_required = 1 WHERE id = (SELECT task_definition_id FROM task_occurrences WHERE id = ?)`, tk.ID)
	first := addChecklistItem(t, f, tk.ID, "Bed made", 0)
	second := addChecklistItem(t, f, tk.ID, "Bag packed", 1)

	rec := f.do(t, child, http.MethodPut, checklistPath(tk.ID, ids.New()),
		map[string]any{"done": true}, ifMatch(tk.Version))
	expectStatus(t, rec, http.StatusNotFound)

	rec = f.do(t, child, http.MethodPost, actionPath(tk.ID, "complete"), nil, ifMatch(tk.Version))
	params := expectError(t, rec, http.StatusConflict, "checklist_incomplete")
	if params["remaining"] != float64(2) {
		t.Errorf("remaining = %v, want 2", params["remaining"])
	}

	ticked := tickItem(t, f, child, tk.ID, first, true, tk.Version)
	rec = f.do(t, child, http.MethodPost, actionPath(tk.ID, "complete"), nil, ifMatch(ticked.Version))
	params = expectError(t, rec, http.StatusConflict, "checklist_incomplete")
	if params["remaining"] != float64(1) {
		t.Errorf("remaining = %v, want 1", params["remaining"])
	}

	ticked = tickItem(t, f, child, tk.ID, second, true, ticked.Version)
	done := runAction(t, f, child, tk.ID, "complete", nil, ticked.Version)
	if c := readCopy(t, f, tk.ID, child.memberID); c.status != statusApproved {
		t.Errorf("status = %s, want approved", c.status)
	}

	rec = f.do(t, child, http.MethodPut, checklistPath(tk.ID, first),
		map[string]any{"done": false}, ifMatch(done.Version))
	expectError(t, rec, http.StatusConflict, "checklist_locked")
}

func TestStaleVersionAndUnassignedMembers(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	other := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)

	rec := f.do(t, child, http.MethodPost, actionPath(tk.ID, "complete"), nil, map[string]string{"If-Match": `"99"`})
	expectError(t, rec, http.StatusPreconditionFailed, "version_conflict")

	submitted := runAction(t, f, child, tk.ID, "complete", nil, tk.Version)

	rec = f.do(t, child, http.MethodPost, actionPath(tk.ID, "complete"), nil, ifMatch(tk.Version))
	expectError(t, rec, http.StatusPreconditionFailed, "version_conflict")

	rec = f.do(t, other, http.MethodPost, actionPath(tk.ID, "complete"), nil, ifMatch(submitted.Version))
	expectStatus(t, rec, http.StatusNotFound)

	rec = f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "approve"),
		memberBody(ids.New()), ifMatch(submitted.Version))
	params := expectError(t, rec, http.StatusBadRequest, "validation_failed")
	if params["field"] != "member_id" || params["reason"] != "not_found" {
		t.Errorf("params = %v, want field member_id and reason not_found", params)
	}

	rec = f.do(t, f.parent, http.MethodPost, actionPath(tk.ID, "approve"),
		map[string]any{"member_id": "nope"}, ifMatch(submitted.Version))
	params = expectError(t, rec, http.StatusBadRequest, "validation_failed")
	if params["field"] != "member_id" || params["reason"] != "invalid" {
		t.Errorf("params = %v, want field member_id and reason invalid", params)
	}
}

func TestMissedOccurrenceReopenGetsNewGracePeriod(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	expires := time.Date(2026, 10, 10, 8, 30, 0, 0, time.UTC)
	setOccurrence(t, f, tk.ID, time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC), &expires)
	events := recordEvents(f)

	reopened := runAction(t, f, f.parent, tk.ID, "reopen", memberBody(child.memberID), tk.Version)
	if c := readCopy(t, f, tk.ID, child.memberID); c.status != taskStatusToDo {
		t.Errorf("copy status = %s, want to_do", c.status)
	}

	var occStatus, occExpires string
	if err := f.db.QueryRow(
		`SELECT status, CAST(expires_at AS TEXT) FROM task_occurrences WHERE id = ?`, tk.ID,
	).Scan(&occStatus, &occExpires); err != nil {
		t.Fatalf("read occurrence: %v", err)
	}
	wantExpires := dbTime(fixtureNow.Add(30 * time.Minute))
	if occStatus != occurrencePastDue || occExpires != wantExpires {
		t.Errorf("occurrence = (%s, %s), want (%s, %s)", occStatus, occExpires, occurrencePastDue, wantExpires)
	}
	if len(*events) != 1 || (*events)[0].Type != EventReopened || !(*events)[0].Missed || (*events)[0].From != taskStatusToDo {
		t.Errorf("events = %+v, want one reopened event for a missed occurrence", *events)
	}

	runAction(t, f, child, tk.ID, "complete", nil, reopened.Version)
	if c := readCopy(t, f, tk.ID, child.memberID); c.status != statusSubmitted || c.late != 1 {
		t.Errorf("copy = %+v, want submitted and completed_late set", c)
	}
}
