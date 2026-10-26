package family

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"nestwall/backend/internal/permissions"
)

func strp(s string) *string { return &s }

// callListMembers runs ListMembers with an optional raw query string.
func callListMembers(t *testing.T, h *Handlers, actor, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/family/members?"+rawQuery, nil)
	req = req.WithContext(context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, actor))
	rec := httptest.NewRecorder()
	h.ListMembers(rec, req)
	return rec
}

func memberUserID(t *testing.T, db *sql.DB, memberID string) string {
	t.Helper()
	var userID string
	if err := db.QueryRow(
		`SELECT u.user_id FROM users u JOIN family_members fm ON fm.user_id = u.id WHERE fm.id = ?`, memberID,
	).Scan(&userID); err != nil {
		t.Fatalf("load user_id: %v", err)
	}
	return userID
}

func memberStatus(t *testing.T, db *sql.DB, memberID string) string {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM family_members WHERE id = ?`, memberID).Scan(&status); err != nil {
		t.Fatalf("load status: %v", err)
	}
	return status
}

func TestListMembersHidesInactiveUnlessRequested(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")
	if _, err := f.db.Exec(`UPDATE family_members SET status = 'inactive' WHERE id = ?`, child); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	ids := func(rec *httptest.ResponseRecorder) map[string]bool {
		t.Helper()
		expectStatus(t, rec, http.StatusOK)
		var got ListMembersResponse
		decodeResp(t, rec, &got)
		out := map[string]bool{}
		for _, m := range got.Members {
			out[m.ID] = true
		}
		return out
	}

	if ids(callListMembers(t, f.h, actor, ""))[child] {
		t.Error("inactive member listed by default")
	}
	if !ids(callListMembers(t, f.h, actor, "include_inactive=true"))[child] {
		t.Error("inactive member missing with include_inactive=true")
	}
}

func TestParentCanDeactivateAndReactivateChild(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{Status: strp("inactive")}), http.StatusNoContent)
	if got := memberStatus(t, f.db, child); got != "inactive" {
		t.Fatalf("status = %q, want inactive", got)
	}

	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{Status: strp("active")}), http.StatusNoContent)
	if got := memberStatus(t, f.db, child); got != "active" {
		t.Fatalf("status = %q, want active", got)
	}
}

func TestParentCannotDeactivateOwnAccount(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID

	rec := call(t, f.h.UpdateMember, actor, actor, UpdateMemberRequest{Status: strp("inactive")})
	expectStatus(t, rec, http.StatusConflict)
	expectErrorCode(t, rec, "cannot_deactivate_self")
	if got := memberStatus(t, f.db, actor); got != "active" {
		t.Fatalf("status = %q, want active", got)
	}
}

func TestParentCanChangeChildUserID(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")
	other := addMember(t, f.db, f.fam.FamilyID, "Child")

	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{UserID: strp("ava.kid")}), http.StatusNoContent)
	if got := memberUserID(t, f.db, child); got != "ava.kid" {
		t.Fatalf("user_id = %q, want ava.kid", got)
	}

	// Case-only change to its own username is not a conflict.
	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{UserID: strp("AVA.Kid")}), http.StatusNoContent)

	rec := call(t, f.h.UpdateMember, actor, other, UpdateMemberRequest{UserID: strp("ava.KID")})
	expectStatus(t, rec, http.StatusConflict)
	expectErrorCode(t, rec, "user_id_taken")

	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{UserID: strp("x")}), http.StatusBadRequest)
}

func TestPINCanOnlyBeSetForChildren(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{PIN: strp("12")}), http.StatusBadRequest)

	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{PIN: strp("4321")}), http.StatusNoContent)
	var pinHash sql.NullString
	if err := f.db.QueryRow(`SELECT pin_hash FROM family_members WHERE id = ?`, child).Scan(&pinHash); err != nil {
		t.Fatalf("load pin_hash: %v", err)
	}
	if !pinHash.Valid || pinHash.String == "" || pinHash.String == "4321" {
		t.Fatalf("pin_hash not stored as a hash: %v", pinHash)
	}

	rec := call(t, f.h.UpdateMember, actor, actor, UpdateMemberRequest{PIN: strp("4321")})
	expectStatus(t, rec, http.StatusBadRequest)
	expectErrorCode(t, rec, "invalid_target")
}

func TestUpdateMemberRejectsEmptyBodyWithNewFieldsAbsent(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	expectStatus(t, call(t, f.h.UpdateMember, actor, child, UpdateMemberRequest{}), http.StatusBadRequest)
}

func TestListMembersReturnsEmailWhenSet(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	if _, err := f.db.ExecContext(context.Background(),
		`UPDATE users SET email = ? WHERE id = (SELECT user_id FROM family_members WHERE id = ?)`,
		"parent@example.test", actor,
	); err != nil {
		t.Fatalf("set email: %v", err)
	}

	rec := call(t, f.h.ListMembers, actor, "", nil)
	expectStatus(t, rec, 200)
	var got ListMembersResponse
	decodeResp(t, rec, &got)

	byID := map[string]MemberDetail{}
	for _, m := range got.Members {
		byID[m.ID] = m
	}
	if m := byID[actor]; m.Email == nil || *m.Email != "parent@example.test" {
		t.Errorf("actor email = %v, want parent@example.test", m.Email)
	}
	if m := byID[actor]; m.RoleID != parentRoleID {
		t.Errorf("actor role_id = %q, want %q", m.RoleID, parentRoleID)
	}
	if m := byID[child]; m.RoleID == "" {
		t.Errorf("child role_id is empty")
	}
	if m := byID[child]; m.Email != nil {
		t.Errorf("child email = %v, want null", *m.Email)
	}
}

const parentRoleID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
