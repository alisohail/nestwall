package family

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

// fixture is one seeded family with its own encrypted database.
type fixture struct {
	db  *sql.DB
	h   *Handlers
	fam testhelper.Family
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db := testhelper.OpenDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return fixture{
		db:  db,
		h:   NewHandlers(db, clock.RealClock{}, logger, t.TempDir()),
		fam: testhelper.OneFamily(t, db),
	}
}

// call runs a handler as the family member actor. pathID fills {id}; body is
// JSON-encoded when not nil.
func call(t *testing.T, fn http.HandlerFunc, actor, pathID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return callIfMatch(t, fn, actor, pathID, "", body)
}

// callIfMatch is call with an If-Match header. An empty ifMatch sends no header.
func callIfMatch(t *testing.T, fn http.HandlerFunc, actor, pathID, ifMatch string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPut, "/", &buf)
	req.SetPathValue("id", pathID)
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	req = req.WithContext(context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, actor))
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, want, rec.Body.String())
	}
}

func decodeResp(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// expectErrorCode checks the error.code field of an error envelope.
func expectErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeResp(t, rec, &env)
	if env.Error.Code != want {
		t.Fatalf("error code = %q, want %q, body = %s", env.Error.Code, want, rec.Body.String())
	}
}

func roleIDByName(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(`SELECT id FROM roles WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("role %q: %v", name, err)
	}
	return id
}

// addMember inserts an active user and family member with the named seeded role.
func addMember(t *testing.T, db *sql.DB, familyID, roleName string) string {
	t.Helper()
	ctx := context.Background()
	userUUID := ids.New()
	isChild := 0
	if roleName == "Child" {
		isChild = 1
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, 'unused', ?, ?)`,
		userUUID, "m"+userUUID[:12], roleName, isChild,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	memberID := ids.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour) VALUES (?, ?, ?, ?, 'active', '#10B981')`,
		memberID, familyID, userUUID, roleIDByName(t, db, roleName),
	); err != nil {
		t.Fatalf("insert family member: %v", err)
	}
	return memberID
}

func TestPermissionCatalogueListsCodes(t *testing.T) {
	f := newFixture(t)
	rec := call(t, f.h.PermissionCatalogue, f.fam.MemberID, "", nil)
	expectStatus(t, rec, http.StatusOK)

	var resp CatalogueResponse
	decodeResp(t, rec, &resp)
	if len(resp.Permissions) == 0 {
		t.Fatal("catalogue is empty")
	}
	for _, p := range resp.Permissions {
		if len(p.Levels) == 0 {
			t.Errorf("permission %s has no levels", p.Code)
		}
		if p.Audience != audienceOf(p.Code) {
			t.Errorf("permission %s audience = %q, want %q", p.Code, p.Audience, audienceOf(p.Code))
		}
	}
	if got := audienceOf("members.manage"); got != "co_parent" {
		t.Errorf("members.manage audience = %q, want co_parent", got)
	}
	if got := audienceOf("roles.manage"); got != "parent" {
		t.Errorf("roles.manage audience = %q, want parent", got)
	}
	if got := audienceOf("tasks.manage"); got != "any" {
		t.Errorf("tasks.manage audience = %q, want any", got)
	}
}

func TestCreateCustomRoleAndNameRules(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID

	rec := call(t, f.h.CreateRole, actor, "", CreateRoleRequest{Name: "Grandparent", Description: "Visits"})
	expectStatus(t, rec, http.StatusCreated)
	var created CreateRoleResponse
	decodeResp(t, rec, &created)
	if created.ID == "" {
		t.Fatal("created role has no ID")
	}

	// Role names are unique, ignoring case.
	expectStatus(t, call(t, f.h.CreateRole, actor, "", CreateRoleRequest{Name: "grandparent"}), http.StatusConflict)
	// A blank name is rejected.
	expectStatus(t, call(t, f.h.CreateRole, actor, "", CreateRoleRequest{Name: "   "}), http.StatusBadRequest)

	// The new custom role can receive a read grant.
	expectStatus(t, call(t, f.h.UpdateRolePermissions, actor, created.ID,
		SetPermissionsRequest{Permissions: map[string]string{"members.view": "view"}}), http.StatusOK)
}

func TestParentRoleIsLocked(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	parent := roleIDByName(t, f.db, parentRoleName)

	expectStatus(t, call(t, f.h.UpdateRolePermissions, actor, parent,
		SetPermissionsRequest{Permissions: map[string]string{}}), http.StatusConflict)
	expectStatus(t, call(t, f.h.ResetRolePermissions, actor, parent, nil), http.StatusConflict)
}

func TestRoleGrantRules(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := roleIDByName(t, f.db, "Child")

	cases := []struct {
		name  string
		grant map[string]string
		want  int
	}{
		{"unknown code is rejected", map[string]string{"no.such.code": "view"}, http.StatusBadRequest},
		{"read code cannot be full", map[string]string{"members.view": "full"}, http.StatusBadRequest},
		{"parent-only code cannot be granted", map[string]string{"members.manage": "allowed"}, http.StatusBadRequest},
		{"parent-only code cannot be denied either", map[string]string{"roles.manage": "denied"}, http.StatusBadRequest},
		{"child can be given a read code", map[string]string{"members.view": "view"}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, f.h.UpdateRolePermissions, actor, child, SetPermissionsRequest{Permissions: tc.grant})
			expectStatus(t, rec, tc.want)
		})
	}
}

func TestResetRestoresDefaults(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := roleIDByName(t, f.db, "Child")

	expectStatus(t, call(t, f.h.UpdateRolePermissions, actor, child,
		SetPermissionsRequest{Permissions: map[string]string{"members.view": "denied"}}), http.StatusOK)
	expectStatus(t, call(t, f.h.ResetRolePermissions, actor, child, nil), http.StatusOK)

	rec := call(t, f.h.ListRoles, actor, "", nil)
	expectStatus(t, rec, http.StatusOK)
	var resp ListRolesResponse
	decodeResp(t, rec, &resp)

	found := false
	for _, role := range resp.Roles {
		if role.Name != "Child" {
			continue
		}
		found = true
		if len(role.Defaults) == 0 {
			t.Fatal("child role has no default grants")
		}
		if !reflect.DeepEqual(role.Permissions, role.Defaults) {
			t.Errorf("after reset grants = %v, want defaults %v", role.Permissions, role.Defaults)
		}
	}
	if !found {
		t.Fatal("child role not listed")
	}
}

func TestMemberOverrides(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	expectStatus(t, call(t, f.h.UpdateMemberPermissions, actor, child,
		SetPermissionsRequest{Permissions: map[string]string{"members.manage": "view"}}), http.StatusBadRequest)

	expectStatus(t, call(t, f.h.UpdateMemberPermissions, actor, child,
		SetPermissionsRequest{Permissions: map[string]string{"members.view": "view"}}), http.StatusOK)

	rec := call(t, f.h.GetMemberPermissions, actor, child, nil)
	expectStatus(t, rec, http.StatusOK)
	var got MemberPermissionsResponse
	decodeResp(t, rec, &got)
	if got.IsParent {
		t.Error("child reported as parent")
	}
	if got.Permissions["members.view"] != "view" {
		t.Errorf("override = %v, want members.view=view", got.Permissions)
	}
}

func TestParentOverridesAreLockedAndUnknownMemberIs404(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID

	expectStatus(t, call(t, f.h.UpdateMemberPermissions, actor, f.fam.MemberID,
		SetPermissionsRequest{Permissions: map[string]string{}}), http.StatusConflict)

	rec := call(t, f.h.GetMemberPermissions, actor, f.fam.MemberID, nil)
	expectStatus(t, rec, http.StatusOK)
	var got MemberPermissionsResponse
	decodeResp(t, rec, &got)
	if !got.IsParent {
		t.Error("parent not reported as parent")
	}

	expectStatus(t, call(t, f.h.GetMemberPermissions, actor, ids.New(), nil), http.StatusNotFound)
	expectStatus(t, call(t, f.h.UpdateMemberPermissions, actor, ids.New(),
		SetPermissionsRequest{Permissions: map[string]string{}}), http.StatusNotFound)
}

func TestStaleRoleVersionIsRejected(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := roleIDByName(t, f.db, "Child")
	grant := SetPermissionsRequest{Permissions: map[string]string{"members.view": "denied"}}

	// Version 1 is the one the editor loaded. The write succeeds and returns version 2.
	rec := callIfMatch(t, f.h.UpdateRolePermissions, actor, child, `"1"`, grant)
	expectStatus(t, rec, http.StatusOK)
	var saved SaveResponse
	decodeResp(t, rec, &saved)
	if saved.Version != 2 {
		t.Fatalf("version = %d, want 2", saved.Version)
	}

	// Version 1 is now stale: another parent saved first.
	rec = callIfMatch(t, f.h.UpdateRolePermissions, actor, child, `"1"`, grant)
	expectStatus(t, rec, http.StatusPreconditionFailed)
	expectErrorCode(t, rec, "version_conflict")

	// A stale reset is rejected too.
	expectStatus(t, callIfMatch(t, f.h.ResetRolePermissions, actor, child, `"1"`, nil), http.StatusPreconditionFailed)

	// The current version is accepted, a weak tag is accepted, and a malformed header is a 400.
	expectStatus(t, callIfMatch(t, f.h.UpdateRolePermissions, actor, child, `"2"`, grant), http.StatusOK)
	expectStatus(t, callIfMatch(t, f.h.UpdateRolePermissions, actor, child, `W/"3"`, grant), http.StatusOK)
	expectStatus(t, callIfMatch(t, f.h.UpdateRolePermissions, actor, child, "abc", grant), http.StatusBadRequest)

	rec = callIfMatch(t, f.h.ResetRolePermissions, actor, child, `"4"`, nil)
	expectStatus(t, rec, http.StatusOK)
	decodeResp(t, rec, &saved)
	if saved.Version != 5 {
		t.Fatalf("version after reset = %d, want 5", saved.Version)
	}
}

func TestStaleMemberVersionIsRejectedAndETagTracksVersion(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	rec := call(t, f.h.GetMemberPermissions, actor, child, nil)
	expectStatus(t, rec, http.StatusOK)
	if etag := rec.Header().Get("ETag"); etag != `"1"` {
		t.Fatalf("ETag = %q, want %q", etag, `"1"`)
	}

	expectStatus(t, callIfMatch(t, f.h.UpdateMemberPermissions, actor, child, `"1"`,
		SetPermissionsRequest{Permissions: map[string]string{"members.view": "view"}}), http.StatusOK)

	// The first save made version 1 stale. The second save must not overwrite it.
	rec = callIfMatch(t, f.h.UpdateMemberPermissions, actor, child, `"1"`,
		SetPermissionsRequest{Permissions: map[string]string{}})
	expectStatus(t, rec, http.StatusPreconditionFailed)
	expectErrorCode(t, rec, "version_conflict")

	rec = call(t, f.h.GetMemberPermissions, actor, child, nil)
	expectStatus(t, rec, http.StatusOK)
	if etag := rec.Header().Get("ETag"); etag != `"2"` {
		t.Fatalf("ETag after save = %q, want %q", etag, `"2"`)
	}
	var got MemberPermissionsResponse
	decodeResp(t, rec, &got)
	if got.Version != 2 || got.Permissions["members.view"] != "view" {
		t.Fatalf("stored state = version %d, overrides %v; want version 2 with members.view=view", got.Version, got.Permissions)
	}
}
