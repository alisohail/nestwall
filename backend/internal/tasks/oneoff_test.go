package tasks

import (
	"encoding/json"
	"net/http"
	"testing"

	"nestwall/backend/internal/platform/ids"
)

const tasksPath = "/api/v1/tasks"

// oneOffChildRole is the seeded Child role from migration 000001.
const oneOffChildRole = "c3d4e5f6-a7b8-9012-cdef-123456789012"

// taskList is the list response.
type taskList struct {
	Tasks      []listItem `json:"tasks"`
	NextCursor *string    `json:"next_cursor"`
}

func decodeTask(t *testing.T, body []byte) task {
	t.Helper()
	var out task
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode task %q: %v", body, err)
	}
	return out
}

// createTask creates a task as the actor and returns it. It fails the test on any status other
// than 201.
func createTask(t *testing.T, f fixture, a actor, body map[string]any) task {
	t.Helper()
	rec := f.do(t, a, http.MethodPost, tasksPath, body, nil)
	expectStatus(t, rec, http.StatusCreated)
	return decodeTask(t, rec.Body.Bytes())
}

// listTasks reads one list page with the given query string, for example "?limit=2".
func listTasks(t *testing.T, f fixture, a actor, query string) taskList {
	t.Helper()
	rec := f.do(t, a, http.MethodGet, tasksPath+query, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var out taskList
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list %q: %v", rec.Body.String(), err)
	}
	return out
}

// oneOffBody returns a valid create body for the given assignees.
func oneOffBody(title, dueDate string, assignees ...string) map[string]any {
	members := []string{}
	members = append(members, assignees...)
	return map[string]any{
		"title":        title,
		"due_date":     dueDate,
		"points":       5,
		"assignee_ids": members,
	}
}

// addChildMember adds an active child to the family and returns the child as an actor.
func addChildMember(t *testing.T, f fixture, familyID string) actor {
	t.Helper()
	userID := ids.New()
	memberID := ids.New()
	execSQL(t, f.db,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, 'unused', 'Ava', 1)`,
		userID, "child_"+userID[:8])
	execSQL(t, f.db,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour) VALUES (?, ?, ?, ?, 'active', '#F59E0B')`,
		memberID, familyID, userID, oneOffChildRole)
	return actor{memberID: memberID, userID: userID}
}

// categoryIDByName returns the ID of a seeded category of a family, found by its name.
func categoryIDByName(t *testing.T, f fixture, familyID, name string) string {
	t.Helper()
	var id string
	if err := f.db.QueryRow(
		`SELECT id FROM task_categories WHERE family_id = ? AND name = ?`, familyID, name,
	).Scan(&id); err != nil {
		t.Fatalf("find category %s: %v", name, err)
	}
	return id
}

// countAudit returns how many audit rows exist for an action on a task.
func countAudit(t *testing.T, f fixture, action, taskID string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE family_id = ? AND action = ? AND target_id = ?`,
		f.a.FamilyID, action, taskID,
	).Scan(&n); err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	return n
}

func TestCreateUsesFamilyZoneForDueTime(t *testing.T) {
	f := newFixture(t)
	execSQL(t, f.db, `UPDATE families SET timezone = 'America/Toronto' WHERE id = ?`, f.a.FamilyID)

	body := oneOffBody("Walk the dog", "2026-10-09", f.parent.memberID)
	body["due_time"] = "17:00"
	rec := f.do(t, f.parent, http.MethodPost, tasksPath, body, nil)
	expectStatus(t, rec, http.StatusCreated)

	got := decodeTask(t, rec.Body.Bytes())
	if got.DueAt != "2026-10-09T21:00:00Z" {
		t.Fatalf("due_at = %q, want 2026-10-09T21:00:00Z (17:00 Toronto)", got.DueAt)
	}
	if got.Version != 1 || rec.Header().Get("ETag") != `"1"` {
		t.Fatalf("version %d, ETag %q; want 1 and \"1\"", got.Version, rec.Header().Get("ETag"))
	}
	if got.Archived || got.Priority == nil || *got.Priority != "normal" {
		t.Fatalf("archived=%v priority=%v; want false and normal", got.Archived, got.Priority)
	}
	if len(got.Assignees) != 1 || got.Assignees[0].Status != "to_do" {
		t.Fatalf("assignees = %+v, want one to_do row", got.Assignees)
	}
	if len(got.AssigneeIDs) != 1 || got.AssigneeIDs[0] != f.parent.memberID {
		t.Fatalf("assignee_ids = %v, want [%s]", got.AssigneeIDs, f.parent.memberID)
	}
}

func TestCreateWithoutDueTimeIsEndOfDay(t *testing.T) {
	f := newFixture(t)
	got := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	if got.DueAt != "2026-10-09T23:59:59Z" {
		t.Fatalf("due_at = %q, want end of day 2026-10-09T23:59:59Z", got.DueAt)
	}
	if got.ExpiresAt != nil {
		t.Fatalf("expires_at = %v, want nil (no expiry set)", *got.ExpiresAt)
	}
}

func TestCreateAppliesPrefillInOrder(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	execSQL(t, f.db,
		`INSERT INTO app_settings (family_id, setting_key, setting_value) VALUES (?, 'defaults.points', '7')`,
		f.a.FamilyID)
	chores := categoryIDByName(t, f, f.a.FamilyID, "Chores")
	activities := categoryIDByName(t, f, f.a.FamilyID, "Activities")
	tpl := createTemplate(t, f, f.parent, map[string]any{
		"name":                 "Tidy room",
		"title":                "From template",
		"category_id":          chores,
		"points":               9,
		"default_assignee_ids": []string{child.memberID},
	})

	t.Run("category supplies points, approval and expiry", func(t *testing.T) {
		body := map[string]any{"title": "Dishes", "due_date": "2026-10-09", "category_id": chores, "assignee_ids": []string{f.parent.memberID}}
		got := createTask(t, f, f.parent, body)
		if got.Points == nil || *got.Points != 5 {
			t.Fatalf("points = %v, want 5 from Chores", got.Points)
		}
		if got.ApprovalRequired == nil || !*got.ApprovalRequired {
			t.Fatalf("approval_required should come from Chores (true)")
		}
		if got.ExpiresAt == nil || *got.ExpiresAt != "2026-10-11T23:59:59Z" {
			t.Fatalf("expires_at = %v, want 2026-10-11T23:59:59Z (2880 minutes after due)", got.ExpiresAt)
		}
	})

	t.Run("family default fills what the category leaves", func(t *testing.T) {
		body := map[string]any{"title": "Art club", "due_date": "2026-10-09", "category_id": activities, "assignee_ids": []string{f.parent.memberID}}
		got := createTask(t, f, f.parent, body)
		if got.Points == nil || *got.Points != 7 {
			t.Fatalf("points = %v, want 7 from family default", got.Points)
		}
	})

	t.Run("family default with no category", func(t *testing.T) {
		body := map[string]any{"title": "Water plants", "due_date": "2026-10-09", "assignee_ids": []string{f.parent.memberID}}
		got := createTask(t, f, f.parent, body)
		if got.Points == nil || *got.Points != 7 {
			t.Fatalf("points = %v, want 7 from family default", got.Points)
		}
	})

	t.Run("template supplies values and assignees when request gives none", func(t *testing.T) {
		templateID := tpl.ID
		body := map[string]any{"template_id": templateID, "due_date": "2026-10-09"}
		got := createTask(t, f, f.parent, body)
		if got.Title == nil || *got.Title != "From template" {
			t.Fatalf("title = %v, want the template title", got.Title)
		}
		if got.Points == nil || *got.Points != 9 {
			t.Fatalf("points = %v, want 9 from template", got.Points)
		}
		if got.CategoryID == nil || *got.CategoryID != chores {
			t.Fatalf("category = %v, want Chores", got.CategoryID)
		}
		if got.TemplateID == nil || *got.TemplateID != templateID {
			t.Fatalf("template_id = %v, want %s", got.TemplateID, templateID)
		}
		if len(got.Assignees) != 1 || got.Assignees[0].MemberID != child.memberID {
			t.Fatalf("assignees = %+v, want the template's child", got.Assignees)
		}
	})

	t.Run("request beats template", func(t *testing.T) {
		body := map[string]any{
			"template_id": tpl.ID, "title": "Mine", "points": 11,
			"due_date": "2026-10-09", "assignee_ids": []string{f.parent.memberID},
		}
		got := createTask(t, f, f.parent, body)
		if got.Title == nil || *got.Title != "Mine" {
			t.Fatalf("title = %v, want Mine", got.Title)
		}
		if got.Points == nil || *got.Points != 11 {
			t.Fatalf("points = %v, want 11 from request", got.Points)
		}
	})
}

func TestCreateValidation(t *testing.T) {
	f := newFixture(t)
	otherChores := categoryIDByName(t, f, f.b.FamilyID, "Chores")

	cases := []struct {
		name   string
		change func(map[string]any)
		field  string
		reason string
	}{
		{"missing title", func(b map[string]any) { delete(b, "title") }, "title", "required"},
		{"blank title", func(b map[string]any) { b["title"] = "   " }, "title", "required"},
		{"missing due date", func(b map[string]any) { delete(b, "due_date") }, "due_date", "required"},
		{"bad due date", func(b map[string]any) { b["due_date"] = "2026-13-01" }, "due_date", "invalid"},
		{"bad due time", func(b map[string]any) { b["due_time"] = "5pm" }, "due_time", "invalid"},
		{"missing points", func(b map[string]any) { delete(b, "points") }, "points", "required"},
		{"negative reminder", func(b map[string]any) { b["reminder_minutes_before"] = -1 }, "reminder_minutes_before", "negative"},
		{"late percent out of range", func(b map[string]any) { b["late_points_percent"] = 101 }, "late_points_percent", "out_of_range"},
		{"bad priority", func(b map[string]any) { b["priority"] = "urgent" }, "priority", "invalid"},
		{"bad part of day", func(b map[string]any) { b["part_of_day"] = "night" }, "part_of_day", "invalid"},
		{"no assignees", func(b map[string]any) { b["assignee_ids"] = []string{} }, "assignee_ids", "required"},
		{"assignee not a UUID", func(b map[string]any) { b["assignee_ids"] = []string{"nope"} }, "assignee_ids", "invalid"},
		{"assignee in another family", func(b map[string]any) { b["assignee_ids"] = []string{f.other.memberID} }, "assignee_ids", "not_found"},
		{"category in another family", func(b map[string]any) { b["category_id"] = otherChores }, "category_id", "not_found"},
		{"template not found", func(b map[string]any) { b["template_id"] = ids.New() }, "template_id", "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := oneOffBody("Tidy room", "2026-10-09", f.parent.memberID)
			tc.change(body)
			rec := f.do(t, f.parent, http.MethodPost, tasksPath, body, nil)
			expectValidation(t, rec, tc.field, tc.reason)
		})
	}
}

func TestCreateRejectsUnknownFields(t *testing.T) {
	f := newFixture(t)
	body := oneOffBody("Tidy room", "2026-10-09", f.parent.memberID)
	body["bogus"] = true
	rec := f.do(t, f.parent, http.MethodPost, tasksPath, body, nil)
	expectError(t, rec, http.StatusBadRequest, "bad_request")
}

func TestListReturnsOneCopyPerPersonWithFilters(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	createTask(t, f, f.parent, oneOffBody("Early", "2026-10-09", f.parent.memberID))
	createTask(t, f, f.parent, oneOffBody("Shared", "2026-10-10", f.parent.memberID, child.memberID))

	all := listTasks(t, f, f.parent, "")
	if len(all.Tasks) != 3 {
		t.Fatalf("list has %d copies, want 3 (one per person)", len(all.Tasks))
	}
	if all.Tasks[0].Title == nil || *all.Tasks[0].Title != "Early" {
		t.Fatalf("first item is not the earliest due task: %+v", all.Tasks[0])
	}

	mine := listTasks(t, f, f.parent, "?member="+child.memberID)
	if len(mine.Tasks) != 1 || mine.Tasks[0].MemberID != child.memberID {
		t.Fatalf("member filter returned %+v, want the child's copy only", mine.Tasks)
	}
	later := listTasks(t, f, f.parent, "?due_from=2026-10-10")
	if len(later.Tasks) != 2 {
		t.Fatalf("due_from filter returned %d copies, want 2", len(later.Tasks))
	}
	none := listTasks(t, f, f.parent, "?status=approved")
	if len(none.Tasks) != 0 {
		t.Fatalf("status filter returned %d copies, want 0", len(none.Tasks))
	}

	for _, query := range []string{"?limit=0", "?limit=201", "?cursor=bogus"} {
		rec := f.do(t, f.parent, http.MethodGet, tasksPath+query, nil, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", query, rec.Code)
		}
	}
	rec := f.do(t, f.parent, http.MethodGet, tasksPath+"?priority=urgent", nil, nil)
	expectValidation(t, rec, "priority", "invalid")
}

func TestListPagesWithoutDuplicates(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	for _, day := range []string{"2026-10-01", "2026-10-02", "2026-10-03"} {
		createTask(t, f, f.parent, oneOffBody("Task "+day, day, f.parent.memberID, child.memberID))
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		query := "?limit=4"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		page := listTasks(t, f, f.parent, query)
		for _, item := range page.Tasks {
			key := item.ID + "/" + item.MemberID
			if seen[key] {
				t.Fatalf("copy %s appears on more than one page", key)
			}
			seen[key] = true
		}
		pages++
		if page.NextCursor == nil {
			break
		}
		if pages > 10 {
			t.Fatal("pagination did not end")
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 6 || pages != 2 {
		t.Fatalf("got %d copies over %d pages, want 6 over 2", len(seen), pages)
	}
}

func TestChildSeesOnlyOwnCopies(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	shared := createTask(t, f, f.parent, oneOffBody("Shared", "2026-10-09", f.parent.memberID, child.memberID))
	private := createTask(t, f, f.parent, oneOffBody("Parent only", "2026-10-09", f.parent.memberID))

	list := listTasks(t, f, child, "")
	if len(list.Tasks) != 1 || list.Tasks[0].ID != shared.ID || list.Tasks[0].MemberID != child.memberID {
		t.Fatalf("child list = %+v, want only its copy of the shared task", list.Tasks)
	}

	rec := f.do(t, child, http.MethodGet, tasksPath+"/"+shared.ID, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if len(got.Assignees) != 1 || got.Assignees[0].MemberID != child.memberID {
		t.Fatalf("child sees assignees %+v, want only itself", got.Assignees)
	}

	expectError(t, f.do(t, child, http.MethodGet, tasksPath+"/"+private.ID, nil, nil), http.StatusNotFound, "not_found")
}

func TestTasksAreHiddenFromOtherFamilies(t *testing.T) {
	f := newFixture(t)
	mine := createTask(t, f, f.parent, oneOffBody("Mine", "2026-10-09", f.parent.memberID))
	path := tasksPath + "/" + mine.ID

	if list := listTasks(t, f, f.other, ""); len(list.Tasks) != 0 {
		t.Fatalf("other family sees %d copies, want 0", len(list.Tasks))
	}
	expectError(t, f.do(t, f.other, http.MethodGet, path, nil, nil), http.StatusNotFound, "not_found")
	expectError(t, f.do(t, f.other, http.MethodPatch, path, map[string]any{"title": "x"}, map[string]string{"If-Match": `"1"`}), http.StatusNotFound, "not_found")
	expectError(t, f.do(t, f.other, http.MethodPost, path+"/archive", nil, map[string]string{"If-Match": `"1"`}), http.StatusNotFound, "not_found")
}

func TestTaskPatchChangesOnlyTheFieldsSent(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Walk the dog", "2026-10-09", f.parent.memberID))

	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+created.ID,
		map[string]any{"title": "Walk Biscuit"}, map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if got.Title == nil || *got.Title != "Walk Biscuit" {
		t.Fatalf("title = %v, want Walk Biscuit", got.Title)
	}
	if got.Points == nil || *got.Points != 5 || got.DueAt != created.DueAt {
		t.Fatalf("points or due_at changed: points=%v due_at=%q", got.Points, got.DueAt)
	}
	if got.Version != 2 || rec.Header().Get("ETag") != `"2"` {
		t.Fatalf("version %d, ETag %q; want 2 and \"2\"", got.Version, rec.Header().Get("ETag"))
	}
	if len(got.Assignees) != 1 {
		t.Fatalf("assignees changed: %+v", got.Assignees)
	}
}

func TestPatchRecomputesDueAt(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	path := tasksPath + "/" + created.ID

	rec := f.do(t, f.parent, http.MethodPatch, path, map[string]any{"due_time": "09:30"}, map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeTask(t, rec.Body.Bytes()); got.DueAt != "2026-10-09T09:30:00Z" {
		t.Fatalf("due_at = %q, want 2026-10-09T09:30:00Z", got.DueAt)
	}

	rec = f.do(t, f.parent, http.MethodPatch, path, map[string]any{"due_time": nil}, map[string]string{"If-Match": `"2"`})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeTask(t, rec.Body.Bytes()); got.DueAt != "2026-10-09T23:59:59Z" {
		t.Fatalf("due_at = %q after clearing due_time, want end of day", got.DueAt)
	}
}

func TestTaskPatchChecksIfMatch(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+created.ID,
		map[string]any{"title": "New"}, map[string]string{"If-Match": `"7"`})
	expectError(t, rec, http.StatusPreconditionFailed, "version_conflict")
}

func TestPatchReplacesAssignees(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	path := tasksPath + "/" + created.ID

	rec := f.do(t, f.parent, http.MethodPatch, path, map[string]any{"assignee_ids": []string{child.memberID}}, map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if len(got.Assignees) != 1 || got.Assignees[0].MemberID != child.memberID {
		t.Fatalf("assignees = %+v, want only the child", got.Assignees)
	}

	rec = f.do(t, f.parent, http.MethodPatch, path, map[string]any{"assignee_ids": []string{}}, map[string]string{"If-Match": `"2"`})
	expectValidation(t, rec, "assignee_ids", "required")
}

func TestPatchRemovesAssigneeWithProgress(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID, child.memberID))
	execSQL(t, f.db,
		`UPDATE task_occurrence_assignees SET status = 'submitted', completed_at = '2026-10-09 10:00:00.000+00:00'
		 WHERE family_id = ? AND family_member_id = ?`,
		f.a.FamilyID, child.memberID)
	path := tasksPath + "/" + created.ID

	rec := f.do(t, f.parent, http.MethodPatch, path,
		map[string]any{"assignee_ids": []string{f.parent.memberID}}, map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if len(got.Assignees) != 1 || got.Assignees[0].MemberID != f.parent.memberID {
		t.Fatalf("assignees = %+v, want only the parent", got.Assignees)
	}
}

func TestArchiveHidesTaskAndBlocksChanges(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	path := tasksPath + "/" + created.ID

	rec := f.do(t, f.parent, http.MethodPost, path+"/archive", nil, map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if !got.Archived || got.Version != 2 || rec.Header().Get("ETag") != `"2"` {
		t.Fatalf("archived=%v version=%d ETag=%q; want true, 2, \"2\"", got.Archived, got.Version, rec.Header().Get("ETag"))
	}

	expectError(t, f.do(t, f.parent, http.MethodPost, path+"/archive", nil, nil), http.StatusConflict, "task_archived")
	expectError(t, f.do(t, f.parent, http.MethodPatch, path,
		map[string]any{"title": "New"}, map[string]string{"If-Match": `"2"`}), http.StatusConflict, "task_archived")

	if list := listTasks(t, f, f.parent, ""); len(list.Tasks) != 0 {
		t.Fatalf("default list shows %d archived copies, want 0", len(list.Tasks))
	}
	shown := listTasks(t, f, f.parent, "?archived=true")
	if len(shown.Tasks) != 1 || !shown.Tasks[0].Archived {
		t.Fatalf("archived=true list = %+v, want the archived copy", shown.Tasks)
	}
}

func TestWritesAreAudited(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	path := tasksPath + "/" + created.ID

	rec := f.do(t, f.parent, http.MethodPatch, path, map[string]any{"title": "Tidy"}, map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)
	rec = f.do(t, f.parent, http.MethodPost, path+"/archive", nil, map[string]string{"If-Match": `"2"`})
	expectStatus(t, rec, http.StatusOK)

	for _, action := range []string{"task.created", "task.updated", "task.archived"} {
		if n := countAudit(t, f, action, created.ID); n != 1 {
			t.Fatalf("audit %s rows = %d, want 1", action, n)
		}
	}
}
