package tasks

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"nestwall/backend/internal/platform/ids"
)

const templatesPath = "/api/v1/task-templates"

// templatePath returns the path of one template.
func templatePath(id string) string {
	return templatesPath + "/" + id
}

// templateList is the list response.
type templateList struct {
	Templates  []template `json:"templates"`
	NextCursor *string    `json:"next_cursor"`
}

func decodeTemplate(t *testing.T, body []byte) template {
	t.Helper()
	var tpl template
	if err := json.Unmarshal(body, &tpl); err != nil {
		t.Fatalf("decode template %q: %v", body, err)
	}
	return tpl
}

// createTemplate creates a template as the actor and returns it. It fails the test on any status other than 201.
func createTemplate(t *testing.T, f fixture, a actor, body map[string]any) template {
	t.Helper()
	rec := f.do(t, a, http.MethodPost, templatesPath, body, nil)
	expectStatus(t, rec, http.StatusCreated)
	return decodeTemplate(t, rec.Body.Bytes())
}

// insertTaskFromTemplate adds a task definition that copies a template's values, as M3-04 will.
// It returns the task ID.
func insertTaskFromTemplate(t *testing.T, f fixture, templateID string) string {
	t.Helper()
	id := ids.New()
	if _, err := f.db.Exec(
		`INSERT INTO task_definitions (id, family_id, template_id, title, time_zone, starts_on, points, created_by)
		 VALUES (?, ?, ?, 'Tidy room', 'Europe/London', '2026-01-05', 15, ?)`,
		id, f.a.FamilyID, templateID, f.parent.userID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	return id
}

func testInt(n int) *int {
	return &n
}

func testBool(b bool) *bool {
	return &b
}

// blankDefaults reports whether no default is set.
func blankDefaults(d Defaults) bool {
	return d.Points == nil && d.ApprovalRequired == nil && d.ExpiresAfterMinutes == nil &&
		d.PenaltyValue == nil && d.PenaltyUnit == nil
}

func templateNames(list []template) []string {
	names := make([]string, 0, len(list))
	for _, tpl := range list {
		if tpl.Name != nil {
			names = append(names, *tpl.Name)
		}
	}
	return names
}

func TestCreateNameOnlyTemplateHasNoDefaults(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.parent, http.MethodPost, templatesPath, map[string]any{"name": "Tidy room"}, nil)
	expectStatus(t, rec, http.StatusCreated)
	if etag := rec.Header().Get("ETag"); etag != `"1"` {
		t.Errorf("ETag = %q, want \"1\"", etag)
	}

	// Unset defaults are null in the JSON, not zero, so the form shows "No default".
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for _, key := range []string{
		"title", "description", "category_id", "part_of_day", "priority", "due_time",
		"reminder_minutes_before", "late_points_percent", "points", "approval_required",
		"expires_after_minutes", "penalty_value", "penalty_unit",
	} {
		if v, ok := raw[key]; !ok || v != nil {
			t.Errorf("%s = %v, want null", key, v)
		}
	}
	if assignees, ok := raw["default_assignee_ids"].([]any); !ok || len(assignees) != 0 {
		t.Errorf("default_assignee_ids = %v, want []", raw["default_assignee_ids"])
	}

	// A task started from this template, with no category, has nothing to prefill.
	tpl := decodeTemplate(t, rec.Body.Bytes())
	if prefill := ResolvePrefill(tpl.Defaults, Defaults{}, Defaults{}); !blankDefaults(prefill) {
		t.Errorf("prefill = %+v, want blank", prefill)
	}
}

func TestPartialTemplatePrefillsOnlyItsSetFields(t *testing.T) {
	f := newFixture(t)
	chores := f.categoryID(t, f.a.FamilyID, "chores")
	tpl := createTemplate(t, f, f.parent, map[string]any{
		"name":        "Tidy room",
		"title":       "  Tidy room  ",
		"category_id": chores,
		"points":      15,
		"due_time":    "17:00",
		"part_of_day": "after_school",
	})

	if tpl.Title == nil || *tpl.Title != "Tidy room" {
		t.Errorf("title = %v, want trimmed Tidy room", tpl.Title)
	}
	if tpl.CategoryID == nil || *tpl.CategoryID != chores {
		t.Errorf("category = %v, want %s", tpl.CategoryID, chores)
	}
	if tpl.Points == nil || *tpl.Points != 15 {
		t.Errorf("points = %v, want 15", tpl.Points)
	}
	if tpl.DueTime == nil || *tpl.DueTime != "17:00" {
		t.Errorf("due time = %v, want 17:00", tpl.DueTime)
	}
	if tpl.Priority != nil || tpl.ApprovalRequired != nil || tpl.ExpiresAfterMinutes != nil {
		t.Errorf("unset fields were filled: priority=%v approval=%v expires=%v",
			tpl.Priority, tpl.ApprovalRequired, tpl.ExpiresAfterMinutes)
	}

	// The template's points win over the category's. Approval is not set on the template, so the category supplies it.
	categoryDefaults := Defaults{Points: testInt(5), ApprovalRequired: testBool(true)}
	prefill := ResolvePrefill(tpl.Defaults, categoryDefaults, Defaults{})
	if prefill.Points == nil || *prefill.Points != 15 {
		t.Errorf("prefill points = %v, want 15", prefill.Points)
	}
	if prefill.ApprovalRequired == nil || !*prefill.ApprovalRequired {
		t.Errorf("prefill approval = %v, want true from the category", prefill.ApprovalRequired)
	}
	if prefill.ExpiresAfterMinutes != nil {
		t.Errorf("prefill expiry = %v, want nil", prefill.ExpiresAfterMinutes)
	}
}

func TestCreateTemplateValidation(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name   string
		body   map[string]any
		field  string
		reason string
	}{
		{"missing name", map[string]any{"points": 5}, "name", "required"},
		{"blank name", map[string]any{"name": "   "}, "name", "required"},
		{"long name", map[string]any{"name": strings.Repeat("n", maxTemplateName+1)}, "name", "too_long"},
		{"long title", map[string]any{"name": "x", "title": strings.Repeat("a", maxTemplateTitle+1)}, "title", "too_long"},
		{"bad category id", map[string]any{"name": "x", "category_id": "not-a-uuid"}, "category_id", "invalid"},
		{"bad part of day", map[string]any{"name": "x", "part_of_day": "night"}, "part_of_day", "invalid"},
		{"bad priority", map[string]any{"name": "x", "priority": "urgent"}, "priority", "invalid"},
		{"due time 24h", map[string]any{"name": "x", "due_time": "24:00"}, "due_time", "invalid"},
		{"due time 12h", map[string]any{"name": "x", "due_time": "5:00 pm"}, "due_time", "invalid"},
		{"negative reminder", map[string]any{"name": "x", "reminder_minutes_before": -1}, "reminder_minutes_before", "negative"},
		{"late points over 100", map[string]any{"name": "x", "late_points_percent": 101}, "late_points_percent", "out_of_range"},
		{"negative points", map[string]any{"name": "x", "points": -1}, "points", "negative"},
		{"penalty value without unit", map[string]any{"name": "x", "penalty_value": 10}, "penalty", "incomplete"},
		{"percent over 100", map[string]any{"name": "x", "penalty_value": 101, "penalty_unit": "percent"}, "penalty_value", "over_100"},
		{"unknown penalty unit", map[string]any{"name": "x", "penalty_value": 1, "penalty_unit": "stars"}, "penalty_unit", "invalid"},
		{"bad assignee id", map[string]any{"name": "x", "default_assignee_ids": []string{"nope"}}, "default_assignee_ids", "invalid"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, f.parent, http.MethodPost, templatesPath, tc.body, nil)
			expectValidation(t, rec, tc.field, tc.reason)
		})
	}

	// Unknown fields are rejected, not ignored.
	rec := f.do(t, f.parent, http.MethodPost, templatesPath, map[string]any{"name": "x", "colour": "red"}, nil)
	expectError(t, rec, http.StatusBadRequest, "bad_request")
}

func TestTemplateReferencesMustBelongToFamily(t *testing.T) {
	f := newFixture(t)
	otherChores := f.categoryID(t, f.b.FamilyID, "chores")

	rec := f.do(t, f.parent, http.MethodPost, templatesPath, map[string]any{"name": "x", "category_id": otherChores}, nil)
	expectValidation(t, rec, "category_id", "not_found")

	rec = f.do(t, f.parent, http.MethodPost, templatesPath,
		map[string]any{"name": "x", "default_assignee_ids": []string{f.other.memberID}}, nil)
	expectValidation(t, rec, "default_assignee_ids", "not_found")

	// PATCH rejects the same reference and leaves the template as it was.
	tpl := createTemplate(t, f, f.parent, map[string]any{"name": "Tidy room"})
	rec = f.do(t, f.parent, http.MethodPatch, templatePath(tpl.ID), map[string]any{"category_id": otherChores}, nil)
	expectValidation(t, rec, "category_id", "not_found")
}

func TestDefaultAssigneesAreStoredOnceAndReplaced(t *testing.T) {
	f := newFixture(t)
	tpl := createTemplate(t, f, f.parent, map[string]any{
		"name":                 "Tidy room",
		"default_assignee_ids": []string{f.parent.memberID, strings.ToUpper(f.parent.memberID)},
	})
	if len(tpl.DefaultAssigneeIDs) != 1 {
		t.Fatalf("assignees = %v, want one after de-duplication", tpl.DefaultAssigneeIDs)
	}

	rec := f.do(t, f.parent, http.MethodPatch, templatePath(tpl.ID),
		map[string]any{"default_assignee_ids": []string{}}, nil)
	expectStatus(t, rec, http.StatusOK)
	if updated := decodeTemplate(t, rec.Body.Bytes()); len(updated.DefaultAssigneeIDs) != 0 {
		t.Errorf("assignees = %v, want none after clearing", updated.DefaultAssigneeIDs)
	}
}

func TestPatchChangesOnlyTheFieldsSent(t *testing.T) {
	f := newFixture(t)
	tpl := createTemplate(t, f, f.parent, map[string]any{
		"name": "Tidy room", "title": "Tidy", "points": 15, "due_time": "17:00",
	})
	path := templatePath(tpl.ID)

	rec := f.do(t, f.parent, http.MethodPatch, path, map[string]any{"title": "Tidy room"}, nil)
	expectStatus(t, rec, http.StatusOK)
	if etag := rec.Header().Get("ETag"); etag != `"2"` {
		t.Errorf("ETag = %q, want \"2\"", etag)
	}
	updated := decodeTemplate(t, rec.Body.Bytes())
	if updated.Title == nil || *updated.Title != "Tidy room" {
		t.Errorf("title = %v, want Tidy room", updated.Title)
	}
	if updated.Points == nil || *updated.Points != 15 || updated.DueTime == nil || *updated.DueTime != "17:00" {
		t.Errorf("unsent fields changed: points=%v due=%v", updated.Points, updated.DueTime)
	}
	if updated.Version != 2 {
		t.Errorf("version = %d, want 2", updated.Version)
	}

	// An explicit null clears a field. Absent fields keep their value.
	rec = f.do(t, f.parent, http.MethodPatch, path, map[string]any{"points": nil, "due_time": nil}, nil)
	expectStatus(t, rec, http.StatusOK)
	cleared := decodeTemplate(t, rec.Body.Bytes())
	if cleared.Points != nil || cleared.DueTime != nil {
		t.Errorf("points=%v due=%v, want both cleared", cleared.Points, cleared.DueTime)
	}
	if cleared.Title == nil || *cleared.Title != "Tidy room" {
		t.Errorf("title = %v, want it kept", cleared.Title)
	}

	// The name is required, so null is rejected.
	rec = f.do(t, f.parent, http.MethodPatch, path, map[string]any{"name": nil}, nil)
	expectValidation(t, rec, "name", "required")
}

func TestPatchChecksIfMatch(t *testing.T) {
	f := newFixture(t)
	tpl := createTemplate(t, f, f.parent, map[string]any{"name": "Tidy room"})
	path := templatePath(tpl.ID)

	rec := f.do(t, f.parent, http.MethodPatch, path, map[string]any{"title": "A"},
		map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)

	// A client that still holds version 1 is refused.
	rec = f.do(t, f.parent, http.MethodPatch, path, map[string]any{"title": "B"},
		map[string]string{"If-Match": `"1"`})
	params := expectError(t, rec, http.StatusPreconditionFailed, "version_conflict")
	if params["version"] != float64(2) {
		t.Errorf("params = %v, want version 2", params)
	}

	rec = f.do(t, f.parent, http.MethodPatch, path, map[string]any{"title": "C"},
		map[string]string{"If-Match": "soon"})
	expectError(t, rec, http.StatusBadRequest, "bad_request")

	// The refused writes changed nothing.
	current := decodeTemplate(t, f.do(t, f.parent, http.MethodGet, path, nil, nil).Body.Bytes())
	if current.Title == nil || *current.Title != "A" || current.Version != 2 {
		t.Errorf("after refused writes: title=%v version=%d, want A and 2", current.Title, current.Version)
	}
}

func TestTemplateChangeDoesNotChangeExistingTask(t *testing.T) {
	f := newFixture(t)
	tpl := createTemplate(t, f, f.parent, map[string]any{"name": "Tidy room", "title": "Tidy room", "points": 15})
	insertTaskFromTemplate(t, f, tpl.ID)

	rec := f.do(t, f.parent, http.MethodPatch, templatePath(tpl.ID),
		map[string]any{"title": "Changed", "points": 20}, nil)
	expectStatus(t, rec, http.StatusOK)

	var title string
	var points int
	if err := f.db.QueryRow(
		`SELECT title, points FROM task_definitions WHERE template_id = ?`, tpl.ID,
	).Scan(&title, &points); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if title != "Tidy room" || points != 15 {
		t.Errorf("task = %q/%d, want the values it was created with (Tidy room/15)", title, points)
	}
}

func TestDeleteTemplateInUseIsRejected(t *testing.T) {
	f := newFixture(t)
	tpl := createTemplate(t, f, f.parent, map[string]any{"name": "Tidy room"})
	taskID := insertTaskFromTemplate(t, f, tpl.ID)
	path := templatePath(tpl.ID)

	rec := f.do(t, f.parent, http.MethodDelete, path, nil, nil)
	params := expectError(t, rec, http.StatusConflict, "template_in_use")
	if params["tasks"] != float64(1) {
		t.Errorf("params = %v, want tasks 1", params)
	}

	// Once the task is gone the template can be deleted.
	if _, err := f.db.Exec(`DELETE FROM task_definitions WHERE id = ?`, taskID); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	expectStatus(t, f.do(t, f.parent, http.MethodDelete, path, nil, nil), http.StatusNoContent)
	expectStatus(t, f.do(t, f.parent, http.MethodGet, path, nil, nil), http.StatusNotFound)
}

func TestDeleteTemplateRemovesItsAssignees(t *testing.T) {
	f := newFixture(t)
	tpl := createTemplate(t, f, f.parent, map[string]any{
		"name":                 "Tidy room",
		"default_assignee_ids": []string{f.parent.memberID},
	})
	expectStatus(t, f.do(t, f.parent, http.MethodDelete, templatePath(tpl.ID), nil, nil), http.StatusNoContent)

	var n int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM task_template_assignees WHERE task_template_id = ?`, tpl.ID,
	).Scan(&n); err != nil {
		t.Fatalf("count assignees: %v", err)
	}
	if n != 0 {
		t.Errorf("assignee rows = %d, want 0 after delete", n)
	}
}

func TestTemplatesAreHiddenFromOtherFamilies(t *testing.T) {
	f := newFixture(t)
	tpl := createTemplate(t, f, f.parent, map[string]any{"name": "Tidy room"})
	path := templatePath(tpl.ID)

	expectStatus(t, f.do(t, f.other, http.MethodGet, path, nil, nil), http.StatusNotFound)
	expectStatus(t, f.do(t, f.other, http.MethodPatch, path, map[string]any{"title": "x"}, nil), http.StatusNotFound)
	expectStatus(t, f.do(t, f.other, http.MethodDelete, path, nil, nil), http.StatusNotFound)

	var list templateList
	rec := f.do(t, f.other, http.MethodGet, templatesPath, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Templates) != 0 {
		t.Errorf("other family sees %d templates, want 0", len(list.Templates))
	}

	// The owner's template is unchanged by the refused requests.
	owner := decodeTemplate(t, f.do(t, f.parent, http.MethodGet, path, nil, nil).Body.Bytes())
	if owner.Title != nil || owner.Version != 1 {
		t.Errorf("owner template title=%v version=%d, want nil and 1", owner.Title, owner.Version)
	}
}

func TestTemplateRoutesNeedActiveMembership(t *testing.T) {
	f := newFixture(t)
	stranger := actor{memberID: ids.New(), userID: f.parent.userID}
	expectError(t, f.do(t, stranger, http.MethodGet, templatesPath, nil, nil), http.StatusForbidden, "no_membership")
	expectError(t, f.do(t, stranger, http.MethodPost, templatesPath, map[string]any{"name": "x"}, nil),
		http.StatusForbidden, "no_membership")
}

func TestListPagesInNameOrder(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"Charlie", "alpha", "Bravo"} {
		createTemplate(t, f, f.parent, map[string]any{"name": name})
	}

	rec := f.do(t, f.parent, http.MethodGet, templatesPath+"?limit=2", nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var first templateList
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if got := templateNames(first.Templates); len(got) != 2 || got[0] != "alpha" || got[1] != "Bravo" {
		t.Errorf("first page = %v, want [alpha Bravo]", got)
	}
	if first.NextCursor == nil {
		t.Fatalf("next_cursor is nil on a full page")
	}

	rec = f.do(t, f.parent, http.MethodGet, templatesPath+"?limit=2&cursor="+*first.NextCursor, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var second templateList
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if got := templateNames(second.Templates); len(got) != 1 || got[0] != "Charlie" {
		t.Errorf("second page = %v, want [Charlie]", got)
	}
	if second.NextCursor != nil {
		t.Errorf("next_cursor = %v on the last page, want nil", *second.NextCursor)
	}

	expectValidation(t, f.do(t, f.parent, http.MethodGet, templatesPath+"?cursor=bogus", nil, nil), "cursor", "invalid")
	expectValidation(t, f.do(t, f.parent, http.MethodGet, templatesPath+"?limit=0", nil, nil), "limit", "invalid")
	expectValidation(t, f.do(t, f.parent, http.MethodGet, templatesPath+"?limit=201", nil, nil), "limit", "invalid")
}

func TestTemplateWritesAreAudited(t *testing.T) {
	f := newFixture(t)
	tpl := createTemplate(t, f, f.parent, map[string]any{"name": "Tidy room"})
	expectStatus(t, f.do(t, f.parent, http.MethodPatch, templatePath(tpl.ID),
		map[string]any{"title": "Tidy"}, nil), http.StatusOK)
	expectStatus(t, f.do(t, f.parent, http.MethodDelete, templatePath(tpl.ID), nil, nil), http.StatusNoContent)

	for _, action := range []string{"task_template.created", "task_template.updated", "task_template.deleted"} {
		var n int
		if err := f.db.QueryRow(
			`SELECT COUNT(*) FROM audit_logs WHERE family_id = ? AND action = ? AND target_id = ?`,
			f.a.FamilyID, action, tpl.ID,
		).Scan(&n); err != nil {
			t.Fatalf("count audit %s: %v", action, err)
		}
		if n != 1 {
			t.Errorf("audit rows for %s = %d, want 1", action, n)
		}
	}
}
