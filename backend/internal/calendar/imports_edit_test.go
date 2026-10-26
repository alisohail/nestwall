package calendar

import (
	"net/http"
	"strings"
	"testing"
)

func TestImportUpdateChangesNameAndColourOnEvents(t *testing.T) {
	f := newFixture(t)
	imp := decodeImport(t, uploadICS(t, f, f.parent, "Term", "term.ics", termICS))

	body := map[string]string{"name": "Autumn term", "colour": "#ff0000"}
	rec := f.do(t, f.parent, http.MethodPatch, "/api/v1/calendar/imports/"+imp.ID, body, nil)
	expectStatus(t, rec, http.StatusNoContent)

	items := listImports(t, f, f.parent)
	if len(items) != 1 || items[0].Name != "Autumn term" || items[0].Colour != "#FF0000" {
		t.Fatalf("list after update = %+v", items)
	}
	if items[0].EventCount != 2 {
		t.Fatalf("event count after update = %d, want 2", items[0].EventCount)
	}

	resp, _ := f.rangeGet(t, f.parent, "from=2026-10-10&to=2026-10-20&calendar_id="+imp.ID)
	if len(resp.Items) == 0 {
		t.Fatal("range has no imported events")
	}
	for _, it := range resp.Items {
		if it.CalendarName == nil || *it.CalendarName != "Autumn term" {
			t.Fatalf("item %q calendar_name = %v, want Autumn term", it.Title, it.CalendarName)
		}
		if it.CalendarColour == nil || *it.CalendarColour != "#FF0000" {
			t.Fatalf("item %q calendar_colour = %v, want #FF0000", it.Title, it.CalendarColour)
		}
	}
}

func TestImportUpdateRejectsBadValues(t *testing.T) {
	f := newFixture(t)
	imp := decodeImport(t, uploadICS(t, f, f.parent, "Term", "term.ics", termICS))
	path := "/api/v1/calendar/imports/" + imp.ID

	tests := []struct {
		name  string
		body  any
		field string
	}{
		{"empty name", map[string]string{"name": "   ", "colour": "#0D9488"}, "name"},
		{"name too long", map[string]string{"name": strings.Repeat("n", maxImportName+1), "colour": "#0D9488"}, "name"},
		{"bad colour", map[string]string{"name": "Term", "colour": "red"}, "colour"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, f.parent, http.MethodPatch, path, tc.body, nil)
			params := expectError(t, rec, http.StatusBadRequest, "validation_failed")
			if params["field"] != tc.field {
				t.Fatalf("field = %v, want %s", params["field"], tc.field)
			}
		})
	}

	unknown := map[string]string{"name": "Term", "colour": "#0D9488", "owner": "everyone"}
	expectError(t, f.do(t, f.parent, http.MethodPatch, path, unknown, nil), http.StatusBadRequest, "bad_request")

	items := listImports(t, f, f.parent)
	if len(items) != 1 || items[0].Name != "Term" {
		t.Fatalf("rejected updates changed the import: %+v", items)
	}
}

func TestImportAudienceChangeAppliesToEvents(t *testing.T) {
	f := newFixture(t)
	imp := decodeImport(t, uploadICS(t, f, f.parent, "Term", "term.ics", termICS))
	path := "/api/v1/calendar/imports/" + imp.ID
	query := "from=2026-10-10&to=2026-10-20&calendar_id=" + imp.ID
	var eventID string
	if err := f.db.QueryRow(`SELECT id FROM calendar_events WHERE import_id = ? LIMIT 1`, imp.ID).Scan(&eventID); err != nil {
		t.Fatalf("read an import event: %v", err)
	}

	if resp, _ := f.rangeGet(t, f.kid, query); len(resp.Items) == 0 {
		t.Fatal("child sees no events before the change")
	}

	parentsOnly := map[string]any{"name": "Term", "colour": imp.Colour, "audience": "parents"}
	expectStatus(t, f.do(t, f.parent, http.MethodPatch, path, parentsOnly, nil), http.StatusNoContent)
	if resp, _ := f.rangeGet(t, f.kid, query); len(resp.Items) != 0 {
		t.Fatalf("child sees %d events after parents only", len(resp.Items))
	}
	expectStatus(t, f.get(t, f.kid, eventID), http.StatusNotFound)
	if resp, _ := f.rangeGet(t, f.parent, query); len(resp.Items) == 0 {
		t.Fatal("parent sees no events after parents only")
	} else {
		for _, it := range resp.Items {
			if !containsID(it.MemberIDs, f.parent.memberID) || containsID(it.MemberIDs, f.kid.memberID) {
				t.Fatalf("item %q member_ids = %v after parents only", it.Title, it.MemberIDs)
			}
		}
	}

	kidOnly := map[string]any{"name": "Term", "colour": imp.Colour, "audience": "specific", "member_ids": []string{f.kid.memberID}}
	expectStatus(t, f.do(t, f.parent, http.MethodPatch, path, kidOnly, nil), http.StatusNoContent)
	if resp, _ := f.rangeGet(t, f.kid, query); len(resp.Items) == 0 {
		t.Fatal("child sees no events after specific to the child")
	}
	items := listImports(t, f, f.parent)
	if len(items) != 1 || items[0].Audience != "specific" || len(items[0].MemberIDs) != 1 || items[0].MemberIDs[0] != f.kid.memberID {
		t.Fatalf("list after specific = %+v", items)
	}

	// A change that leaves audience out keeps it, and only the wall switch moves.
	wallOff := map[string]any{"name": "Term (wall off)", "colour": imp.Colour, "show_on_wall": false}
	expectStatus(t, f.do(t, f.parent, http.MethodPatch, path, wallOff, nil), http.StatusNoContent)
	items = listImports(t, f, f.parent)
	if items[0].Audience != "specific" || len(items[0].MemberIDs) != 1 || items[0].ShowOnWall {
		t.Fatalf("list after wall-only update = %+v", items[0])
	}
	if resp, _ := f.rangeGet(t, f.kid, query); len(resp.Items) == 0 {
		t.Fatal("child lost events after a wall-only update")
	}

	everyone := map[string]any{"name": "Term", "colour": imp.Colour, "audience": "everyone"}
	expectStatus(t, f.do(t, f.parent, http.MethodPatch, path, everyone, nil), http.StatusNoContent)
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_event_members WHERE family_id = ?`, f.a.FamilyID); n != 0 {
		t.Fatalf("member rows after everyone = %d, want 0", n)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_import_members WHERE import_id = ?`, imp.ID); n != 0 {
		t.Fatalf("import member rows after everyone = %d, want 0", n)
	}
	if resp, _ := f.rangeGet(t, f.kid, query); len(resp.Items) == 0 {
		t.Fatal("child sees no events after everyone")
	}
	expectStatus(t, f.get(t, f.kid, eventID), http.StatusOK)
}

func TestImportAudienceUpdateRejectsBadValues(t *testing.T) {
	f := newFixture(t)
	imp := decodeImport(t, uploadICS(t, f, f.parent, "Term", "term.ics", termICS))
	path := "/api/v1/calendar/imports/" + imp.ID

	tests := []struct {
		name     string
		audience string
		members  []string
		field    string
	}{
		{"unknown audience", "bogus", nil, "audience"},
		{"specific without people", "specific", nil, "member_ids"},
		{"specific with other family", "specific", []string{f.other.memberID}, "member_ids"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"name": "Term", "colour": imp.Colour, "audience": tc.audience, "member_ids": tc.members}
			params := expectError(t, f.do(t, f.parent, http.MethodPatch, path, body, nil), http.StatusBadRequest, "validation_failed")
			if params["field"] != tc.field {
				t.Fatalf("field = %v, want %s", params["field"], tc.field)
			}
		})
	}

	items := listImports(t, f, f.parent)
	if len(items) != 1 || items[0].Audience != "everyone" || len(items[0].MemberIDs) != 0 {
		t.Fatalf("rejected audience changed the import: %+v", items)
	}
}

func TestImportUpdateAccessControl(t *testing.T) {
	f := newFixture(t)
	imp := decodeImport(t, uploadICS(t, f, f.parent, "Term", "term.ics", termICS))
	path := "/api/v1/calendar/imports/" + imp.ID
	body := map[string]string{"name": "Hijacked", "colour": "#000000"}

	if rec := f.do(t, actor{}, http.MethodPatch, path, body, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous update status = %d, want 401", rec.Code)
	}
	if rec := f.do(t, f.kid, http.MethodPatch, path, body, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("child update status = %d, want 403", rec.Code)
	}
	if rec := f.do(t, f.other, http.MethodPatch, path, body, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other family update status = %d, want 404", rec.Code)
	}
	if rec := f.do(t, f.parent, http.MethodPatch, "/api/v1/calendar/imports/not-a-uuid", body, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id update status = %d, want 404", rec.Code)
	}

	items := listImports(t, f, f.parent)
	if len(items) != 1 || items[0].Name != "Term" || items[0].Colour != defaultColour {
		t.Fatalf("refused updates changed the import: %+v", items)
	}
}
