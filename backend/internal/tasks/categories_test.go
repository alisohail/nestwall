package tasks

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"nestwall/backend/internal/platform/ids"
)

const categoriesPath = "/api/v1/family/task-categories"

// categoryPath returns the path of one category.
func categoryPath(id string) string {
	return categoriesPath + "/" + id
}

func listCategories(t *testing.T, f fixture, a actor) []category {
	t.Helper()
	rec := f.do(t, a, http.MethodGet, categoriesPath, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var list struct {
		Categories []category `json:"categories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return list.Categories
}

func decodeCategory(t *testing.T, body []byte) category {
	t.Helper()
	var c category
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatalf("decode category %q: %v", body, err)
	}
	return c
}

func TestListReturnsDefaultCategoriesInOrder(t *testing.T) {
	f := newFixture(t)
	list := listCategories(t, f, f.parent)

	want := []string{"Chores", "School work", "Pets", "Outdoor", "Self-care", "Activities", "Family"}
	if len(list) != len(want) {
		t.Fatalf("got %d categories, want %d", len(list), len(want))
	}
	for i, name := range want {
		if list[i].Name != name {
			t.Errorf("category %d = %q, want %q", i, list[i].Name, name)
		}
	}

	chores := list[0]
	if chores.SeedKey == nil || *chores.SeedKey != "chores" {
		t.Errorf("seed key = %v, want chores", chores.SeedKey)
	}
	if chores.Points == nil || *chores.Points != 5 || chores.ApprovalRequired == nil || !*chores.ApprovalRequired ||
		chores.ExpiresAfterMinutes == nil || *chores.ExpiresAfterMinutes != 2880 {
		t.Errorf("chores defaults = %+v, want 5 points, approval, 2880 minutes", chores.Defaults)
	}
	if chores.BadgeKey == nil || *chores.BadgeKey != "tidy_pro" {
		t.Errorf("badge = %v, want tidy_pro", chores.BadgeKey)
	}

	pets := list[2]
	if pets.ApprovalRequired != nil || pets.ExpiresAfterMinutes != nil {
		t.Errorf("pets approval/expiry = %v/%v, want nil", pets.ApprovalRequired, pets.ExpiresAfterMinutes)
	}
	family := list[6]
	if family.Points != nil || family.BadgeKey != nil {
		t.Errorf("family points/badge = %v/%v, want nil", family.Points, family.BadgeKey)
	}
}

func TestListNeedsActiveMembership(t *testing.T) {
	f := newFixture(t)
	stranger := actor{memberID: ids.New(), userID: f.parent.userID}
	rec := f.do(t, stranger, http.MethodGet, categoriesPath, nil, nil)
	expectError(t, rec, http.StatusForbidden, "no_membership")
}

func TestCreateCategoryAppendsAndReturnsETag(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.parent, http.MethodPost, categoriesPath, map[string]any{
		"name":   "  Garden  ",
		"emoji":  "\U0001F331",
		"colour": "#16A34A",
		"points": 4,
	}, nil)
	expectStatus(t, rec, http.StatusCreated)

	if etag := rec.Header().Get("ETag"); etag != `"1"` {
		t.Errorf("ETag = %q, want \"1\"", etag)
	}
	c := decodeCategory(t, rec.Body.Bytes())
	if c.Name != "Garden" {
		t.Errorf("name = %q, want trimmed Garden", c.Name)
	}
	if c.SortOrder != 8 {
		t.Errorf("sort order = %d, want 8 (after the seven defaults)", c.SortOrder)
	}
	if c.Points == nil || *c.Points != 4 {
		t.Errorf("points = %v, want 4", c.Points)
	}
	if c.SeedKey != nil {
		t.Errorf("seed key = %v, want nil for a custom category", *c.SeedKey)
	}
	if c.Version != 1 {
		t.Errorf("version = %d, want 1", c.Version)
	}
}

func TestCreateCategoryRejectsNameTakenIgnoringCase(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.parent, http.MethodPost, categoriesPath, map[string]any{
		"name": "CHORES", "emoji": "\U0001F9F9", "colour": "#0F766E",
	}, nil)
	params := expectError(t, rec, http.StatusConflict, "name_taken")
	if params["field"] != "name" {
		t.Errorf("params = %v, want field name", params)
	}
}

func TestCreateCategoryNameIsScopedToFamily(t *testing.T) {
	f := newFixture(t)
	body := map[string]any{"name": "Garden", "emoji": "\U0001F331", "colour": "#16A34A"}

	// Family B creates "Garden" first. Family A can still use the same name.
	expectStatus(t, f.do(t, f.other, http.MethodPost, categoriesPath, body, nil), http.StatusCreated)
	expectStatus(t, f.do(t, f.parent, http.MethodPost, categoriesPath, body, nil), http.StatusCreated)

	// Within family A the name is still taken.
	expectError(t, f.do(t, f.parent, http.MethodPost, categoriesPath, body, nil), http.StatusConflict, "name_taken")
}

func TestCreateCategoryValidation(t *testing.T) {
	f := newFixture(t)
	base := func() map[string]any {
		return map[string]any{"name": "Garden", "emoji": "\U0001F331", "colour": "#16A34A"}
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
		field  string
		reason string
	}{
		{"blank name", func(b map[string]any) { b["name"] = "  " }, "name", "required"},
		{"long name", func(b map[string]any) { b["name"] = strings.Repeat("n", 81) }, "name", "too_long"},
		{"bad colour", func(b map[string]any) { b["colour"] = "blue" }, "colour", "invalid"},
		{"bad badge key", func(b map[string]any) { b["badge_key"] = "Pet Pal" }, "badge_key", "invalid"},
		{"penalty without unit", func(b map[string]any) { b["penalty_value"] = 5 }, "penalty", "incomplete"},
		{"percent over 100", func(b map[string]any) {
			b["penalty_value"] = 150
			b["penalty_unit"] = "percent"
		}, "penalty_value", "over_100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := base()
			tc.mutate(body)
			rec := f.do(t, f.parent, http.MethodPost, categoriesPath, body, nil)
			expectValidation(t, rec, tc.field, tc.reason)
		})
	}

	// Unknown fields are rejected, so a misspelled field is not silently dropped.
	body := base()
	body["colour_typo"] = "#FFFFFF"
	rec := f.do(t, f.parent, http.MethodPost, categoriesPath, body, nil)
	expectError(t, rec, http.StatusBadRequest, "bad_request")
}

func TestUpdateCategoryUsesVersion(t *testing.T) {
	f := newFixture(t)
	chores := f.categoryID(t, f.a.FamilyID, "chores")
	update := map[string]any{"name": "Housework", "emoji": "\U0001F9F9", "colour": "#0F766E", "points": 6}

	rec := f.do(t, f.parent, http.MethodPut, categoryPath(chores), update,
		map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)
	if etag := rec.Header().Get("ETag"); etag != `"2"` {
		t.Errorf("ETag after update = %q, want \"2\"", etag)
	}
	updated := decodeCategory(t, rec.Body.Bytes())
	if updated.Name != "Housework" || updated.Points == nil || *updated.Points != 6 {
		t.Errorf("updated = %+v, want Housework with 6 points", updated)
	}
	if updated.SortOrder != 1 {
		t.Errorf("sort order = %d, want 1 kept when sort_order is omitted", updated.SortOrder)
	}

	// A stale version is rejected with the current version.
	rec = f.do(t, f.parent, http.MethodPut, categoryPath(chores), update,
		map[string]string{"If-Match": `"1"`})
	params := expectError(t, rec, http.StatusPreconditionFailed, "version_conflict")
	if params["version"] != float64(2) {
		t.Errorf("conflict version = %v, want 2", params["version"])
	}

	// Without If-Match the write goes ahead.
	rec = f.do(t, f.parent, http.MethodPut, categoryPath(chores), update, nil)
	expectStatus(t, rec, http.StatusOK)

	rec = f.do(t, f.parent, http.MethodPut, categoryPath(chores), update,
		map[string]string{"If-Match": "abc"})
	expectError(t, rec, http.StatusBadRequest, "bad_request")
}

func TestUpdateCategoryName(t *testing.T) {
	f := newFixture(t)
	chores := f.categoryID(t, f.a.FamilyID, "chores")

	// Changing only the case of its own name is allowed.
	rec := f.do(t, f.parent, http.MethodPut, categoryPath(chores),
		map[string]any{"name": "CHORES", "emoji": "\U0001F9F9", "colour": "#0F766E"}, nil)
	expectStatus(t, rec, http.StatusOK)

	// Taking another category's name is not.
	pets := f.categoryID(t, f.a.FamilyID, "pets")
	rec = f.do(t, f.parent, http.MethodPut, categoryPath(pets),
		map[string]any{"name": "chores", "emoji": "\U0001F415", "colour": "#F59E0B"}, nil)
	expectError(t, rec, http.StatusConflict, "name_taken")
}

func TestCategoriesAreScopedToFamily(t *testing.T) {
	f := newFixture(t)
	otherPets := f.categoryID(t, f.b.FamilyID, "pets")
	body := map[string]any{"name": "Stolen", "emoji": "\U0001F415", "colour": "#F59E0B"}

	expectError(t, f.do(t, f.parent, http.MethodPut, categoryPath(otherPets), body, nil),
		http.StatusNotFound, "not_found")
	expectError(t, f.do(t, f.parent, http.MethodDelete, categoryPath(otherPets), nil, nil),
		http.StatusNotFound, "not_found")
	expectError(t, f.do(t, f.parent, http.MethodPut, categoryPath(ids.New()), body, nil),
		http.StatusNotFound, "not_found")
	expectError(t, f.do(t, f.parent, http.MethodPut, categoryPath("not-a-uuid"), body, nil),
		http.StatusNotFound, "not_found")

	var name string
	if err := f.db.QueryRow(`SELECT name FROM task_categories WHERE id = ?`, otherPets).Scan(&name); err != nil {
		t.Fatalf("read other family's category: %v", err)
	}
	if name != "Pets" {
		t.Errorf("other family's category name = %q, want Pets", name)
	}
}

func TestDeleteUnusedCategory(t *testing.T) {
	f := newFixture(t)
	activities := f.categoryID(t, f.a.FamilyID, "activities")

	rec := f.do(t, f.parent, http.MethodDelete, categoryPath(activities), nil, nil)
	expectStatus(t, rec, http.StatusNoContent)

	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM task_categories WHERE id = ?`, activities).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("category still exists after delete")
	}
}

func TestDeleteCategoryNeedsCurrentVersionWhenSent(t *testing.T) {
	f := newFixture(t)
	activities := f.categoryID(t, f.a.FamilyID, "activities")
	rec := f.do(t, f.parent, http.MethodDelete, categoryPath(activities), nil,
		map[string]string{"If-Match": `"9"`})
	expectError(t, rec, http.StatusPreconditionFailed, "version_conflict")
}

func TestDeleteCategoryInUseMovesItems(t *testing.T) {
	f := newFixture(t)
	chores := f.categoryID(t, f.a.FamilyID, "chores")
	school := f.categoryID(t, f.a.FamilyID, "school_work")
	otherPets := f.categoryID(t, f.b.FamilyID, "pets")

	tplID := ids.New()
	defID := ids.New()
	execSQL(t, f.db, `INSERT INTO task_templates (id, family_id, name, created_by, category_id)
		VALUES (?, ?, 'Dishes', ?, ?)`, tplID, f.a.FamilyID, f.a.ParentUserID, chores)
	execSQL(t, f.db, `INSERT INTO task_definitions (id, family_id, title, time_zone, starts_on, points, created_by, category_id)
		VALUES (?, ?, 'Dishes', 'UTC', '2026-10-01', 5, ?, ?)`, defID, f.a.FamilyID, f.a.ParentUserID, chores)

	// Without move_to the delete is refused, with the counts of what uses the category.
	rec := f.do(t, f.parent, http.MethodDelete, categoryPath(chores), nil, nil)
	params := expectError(t, rec, http.StatusConflict, "category_in_use")
	if params["templates"] != float64(1) || params["tasks"] != float64(1) {
		t.Fatalf("in-use counts = %v, want templates 1 and tasks 1", params)
	}

	// move_to cannot be the category itself.
	rec = f.do(t, f.parent, http.MethodDelete, categoryPath(chores)+"?move_to="+chores, nil, nil)
	expectValidation(t, rec, "move_to", "invalid")

	// move_to must be in the same family.
	rec = f.do(t, f.parent, http.MethodDelete, categoryPath(chores)+"?move_to="+otherPets, nil, nil)
	expectValidation(t, rec, "move_to", "not_found")

	// A valid move_to moves the template and the task, then deletes the category.
	rec = f.do(t, f.parent, http.MethodDelete, categoryPath(chores)+"?move_to="+school, nil, nil)
	expectStatus(t, rec, http.StatusNoContent)

	for _, q := range []string{
		`SELECT category_id FROM task_templates WHERE id = '` + tplID + `'`,
		`SELECT category_id FROM task_definitions WHERE id = '` + defID + `'`,
	} {
		var got string
		if err := f.db.QueryRow(q).Scan(&got); err != nil {
			t.Fatalf("read moved row: %v", err)
		}
		if got != school {
			t.Errorf("category after move = %s, want %s", got, school)
		}
	}
}

// execSQL runs a statement and fails the test on error.
func execSQL(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
