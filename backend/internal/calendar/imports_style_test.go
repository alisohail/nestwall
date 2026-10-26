package calendar

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// uploadImport posts a multipart import with the given form fields as the actor. A nil content
// sends no file part.
func uploadImport(t *testing.T, f fixture, a actor, fields url.Values, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for key, values := range fields {
		for _, value := range values {
			if err := mw.WriteField(key, value); err != nil {
				t.Fatalf("write %s: %v", key, err)
			}
		}
	}
	if content != nil {
		part, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("create file part: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write file part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/calendar/imports", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if a.cookie != nil {
		req.AddCookie(a.cookie)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestImportColourDefaultsAndNormalises(t *testing.T) {
	f := newFixture(t)

	def := decodeImport(t, uploadImport(t, f, f.parent, url.Values{"name": {"Default"}}, "a.ics", termICS))
	if def.Colour != "#0D9488" || def.Audience != "everyone" || len(def.MemberIDs) != 0 {
		t.Fatalf("default import = %+v", def)
	}

	custom := decodeImport(t, uploadImport(t, f, f.parent, url.Values{"name": {"Club"}, "colour": {"#ff8800"}}, "b.ics", termICS))
	if custom.Colour != "#FF8800" {
		t.Fatalf("colour = %q, want #FF8800", custom.Colour)
	}

	items := listImports(t, f, f.parent)
	if len(items) != 2 {
		t.Fatalf("list = %+v", items)
	}
	for _, it := range items {
		if it.Colour == "" || it.Audience == "" || it.MemberIDs == nil {
			t.Fatalf("list item missing style fields: %+v", it)
		}
	}

	resp, _ := f.rangeGet(t, f.parent, "from=2026-10-10&to=2026-10-20&calendar_id="+custom.ID)
	if len(resp.Items) == 0 {
		t.Fatal("range has no items for the custom import")
	}
	for _, it := range resp.Items {
		if it.CalendarColour == nil || *it.CalendarColour != "#FF8800" {
			t.Fatalf("item %q calendar_colour = %v, want #FF8800", it.Title, it.CalendarColour)
		}
	}
}

func TestImportStyleRejectsBadValues(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name   string
		fields url.Values
	}{
		{"colour not hex", url.Values{"name": {"A"}, "colour": {"teal"}}},
		{"colour too short", url.Values{"name": {"A"}, "colour": {"#FFF"}}},
		{"unknown audience", url.Values{"name": {"A"}, "audience": {"kids"}}},
		{"specific without people", url.Values{"name": {"A"}, "audience": {"specific"}}},
		{"specific with other family", url.Values{"name": {"A"}, "audience": {"specific"}, "member_ids": {f.other.memberID}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expectError(t, uploadImport(t, f, f.parent, tc.fields, "a.ics", termICS), http.StatusBadRequest, "validation_failed")
		})
	}
	if items := listImports(t, f, f.parent); len(items) != 0 {
		t.Fatalf("rejected imports left behind: %+v", items)
	}
}

func TestImportShowOnWallDefaultsAndRoundTrips(t *testing.T) {
	f := newFixture(t)

	def := decodeImport(t, uploadImport(t, f, f.parent, url.Values{"name": {"Default"}}, "a.ics", termICS))
	if !def.ShowOnWall {
		t.Fatalf("default show_on_wall = false, want true")
	}

	off := decodeImport(t, uploadImport(t, f, f.parent, url.Values{"name": {"Quiet"}, "show_on_wall": {"false"}}, "b.ics", termICS))
	if off.ShowOnWall {
		t.Fatalf("created show_on_wall = true, want false")
	}

	items := listImports(t, f, f.parent)
	byID := map[string]importResponse{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if !byID[def.ID].ShowOnWall || byID[off.ID].ShowOnWall {
		t.Fatalf("listed show_on_wall = %+v", items)
	}
}

func TestImportShowOnWallRejectsBadValue(t *testing.T) {
	f := newFixture(t)
	expectError(t, uploadImport(t, f, f.parent, url.Values{"name": {"A"}, "show_on_wall": {"maybe"}}, "a.ics", termICS), http.StatusBadRequest, "validation_failed")
	if items := listImports(t, f, f.parent); len(items) != 0 {
		t.Fatalf("rejected import left behind: %+v", items)
	}
}

func TestImportRefreshDefaultsAndValidates(t *testing.T) {
	f := newFixture(t)

	def := decodeImport(t, uploadImport(t, f, f.parent, url.Values{"name": {"Default"}}, "a.ics", termICS))
	if def.RefreshMinutes != 30 {
		t.Fatalf("default refresh = %d, want 30", def.RefreshMinutes)
	}

	hourly := decodeImport(t, uploadImport(t, f, f.parent, url.Values{"name": {"Hourly"}, "refresh_minutes": {"60"}}, "b.ics", termICS))
	if hourly.RefreshMinutes != 60 {
		t.Fatalf("created refresh = %d, want 60", hourly.RefreshMinutes)
	}
	for _, it := range listImports(t, f, f.parent) {
		if it.ID == hourly.ID && it.RefreshMinutes != 60 {
			t.Fatalf("listed refresh = %d, want 60", it.RefreshMinutes)
		}
	}

	for _, bad := range []string{"45", "soon", "0"} {
		expectError(t, uploadImport(t, f, f.parent, url.Values{"name": {"A"}, "refresh_minutes": {bad}}, "a.ics", termICS), http.StatusBadRequest, "validation_failed")
	}
	if items := listImports(t, f, f.parent); len(items) != 2 {
		t.Fatalf("rejected refresh left imports behind: %+v", items)
	}
}
func TestImportSpecificAudienceIsCopiedToEvents(t *testing.T) {
	f := newFixture(t)
	rec := uploadImport(t, f, f.parent, url.Values{
		"name":       {"Kid club"},
		"audience":   {"specific"},
		"member_ids": {f.kid.memberID},
	}, "club.ics", termICS)
	expectStatus(t, rec, http.StatusCreated)
	imp := decodeImport(t, rec)
	if imp.Audience != "specific" || len(imp.MemberIDs) != 1 || imp.MemberIDs[0] != f.kid.memberID {
		t.Fatalf("import audience = %q %v", imp.Audience, imp.MemberIDs)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_import_members WHERE import_id = ?`, imp.ID); n != 1 {
		t.Fatalf("import member rows = %d, want 1", n)
	}

	resp, _ := f.rangeGet(t, f.parent, "from=2026-10-10&to=2026-10-20&calendar_id="+imp.ID)
	if len(resp.Items) == 0 {
		t.Fatal("range has no items")
	}
	for _, it := range resp.Items {
		if len(it.MemberIDs) != 1 || it.MemberIDs[0] != f.kid.memberID {
			t.Fatalf("item %q member_ids = %v, want the kid", it.Title, it.MemberIDs)
		}
	}

	if rec := f.do(t, f.parent, http.MethodDelete, "/api/v1/calendar/imports/"+imp.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_import_members WHERE family_id = ?`, f.a.FamilyID); n != 0 {
		t.Fatalf("import member rows after delete = %d, want 0", n)
	}
}

func TestImportParentsAudienceResolvesParents(t *testing.T) {
	f := newFixture(t)
	rec := uploadImport(t, f, f.parent, url.Values{"name": {"Grown-ups"}, "audience": {"parents"}}, "p.ics", termICS)
	expectStatus(t, rec, http.StatusCreated)
	imp := decodeImport(t, rec)
	if imp.Audience != "parents" {
		t.Fatalf("audience = %q", imp.Audience)
	}
	if !containsID(imp.MemberIDs, f.parent.memberID) || containsID(imp.MemberIDs, f.kid.memberID) {
		t.Fatalf("parents audience = %v; want the parent and not the child", imp.MemberIDs)
	}

	resp, _ := f.rangeGet(t, f.parent, "from=2026-10-10&to=2026-10-20&calendar_id="+imp.ID)
	if len(resp.Items) == 0 {
		t.Fatal("range has no items")
	}
	for _, it := range resp.Items {
		if !containsID(it.MemberIDs, f.parent.memberID) || containsID(it.MemberIDs, f.kid.memberID) {
			t.Fatalf("item %q member_ids = %v", it.Title, it.MemberIDs)
		}
	}
}
