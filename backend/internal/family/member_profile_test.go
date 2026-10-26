package family

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"nestwall/backend/internal/auth"
)

func memberEmail(t *testing.T, f fixture, memberID string) sql.NullString {
	t.Helper()
	var email sql.NullString
	if err := f.db.QueryRow(
		`SELECT u.email FROM users u JOIN family_members fm ON fm.user_id = u.id WHERE fm.id = ?`, memberID,
	).Scan(&email); err != nil {
		t.Fatalf("load email: %v", err)
	}
	return email
}

func memberProfile(t *testing.T, f fixture, memberID string) (passwordHash string, mustChange bool, dob, gender sql.NullString) {
	t.Helper()
	if err := f.db.QueryRow(
		`SELECT u.password_hash, u.must_change_password, u.date_of_birth, u.gender
		 FROM users u JOIN family_members fm ON fm.user_id = u.id WHERE fm.id = ?`, memberID,
	).Scan(&passwordHash, &mustChange, &dob, &gender); err != nil {
		t.Fatalf("load profile: %v", err)
	}
	return passwordHash, mustChange, dob, gender
}

func TestParentCannotClearEmail(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	if _, err := f.db.ExecContext(context.Background(),
		`UPDATE users SET email = 'parent@example.test' WHERE id = (SELECT user_id FROM family_members WHERE id = ?)`, actor,
	); err != nil {
		t.Fatalf("seed email: %v", err)
	}

	rec := call(t, f.h.UpdateMember, actor, actor, UpdateMemberRequest{Email: strp("  ")})
	expectStatus(t, rec, http.StatusBadRequest)
	expectErrorCode(t, rec, "validation_failed")
	if got := memberEmail(t, f, actor); !got.Valid || got.String != "parent@example.test" {
		t.Fatalf("parent email changed to %v", got)
	}
}

func TestChildEmailIsOptionalAndNormalised(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{Email: strp("  Kid@Example.TEST ")}), http.StatusNoContent)
	if got := memberEmail(t, f, child); !got.Valid || got.String != "kid@example.test" {
		t.Fatalf("email = %v, want kid@example.test", got)
	}

	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{Email: strp("")}), http.StatusNoContent)
	if got := memberEmail(t, f, child); got.Valid {
		t.Fatalf("email = %q, want NULL after clearing", got.String)
	}
}

func TestUpdateMemberRejectsInvalidProfileFields(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	cases := map[string]UpdateMemberRequest{
		"email":    {Email: strp("not-an-email")},
		"dob":      {DateOfBirth: strp("2999-01-01")},
		"dob_text": {DateOfBirth: strp("01/02/1990")},
		"gender":   {Gender: strp("other")},
		"password": {Password: strp("short")},
	}
	for name, body := range cases {
		rec := call(t, f.h.UpdateMember, actor, child, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400, body = %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestUpdateMemberStoresBirthDateAndGender(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{DateOfBirth: strp("2015-06-30"), Gender: strp("prefer_not_to_say")}), http.StatusNoContent)
	_, _, dob, gender := memberProfile(t, f, child)
	if dob.String != "2015-06-30" || gender.String != "prefer_not_to_say" {
		t.Fatalf("dob=%v gender=%v", dob, gender)
	}

	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{DateOfBirth: strp(""), Gender: strp("")}), http.StatusNoContent)
	_, _, dob, gender = memberProfile(t, f, child)
	if dob.Valid || gender.Valid {
		t.Fatalf("dob=%v gender=%v, want NULL after clearing", dob, gender)
	}
}

func TestAdminSetsMemberPasswordWithoutForcingChange(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")
	if _, err := f.db.ExecContext(context.Background(),
		`UPDATE users SET must_change_password = 1 WHERE id = (SELECT user_id FROM family_members WHERE id = ?)`, child,
	); err != nil {
		t.Fatalf("seed flag: %v", err)
	}

	expectStatus(t, call(t, f.h.UpdateMember, actor, child,
		UpdateMemberRequest{Password: strp("fresh-pass-123")}), http.StatusNoContent)

	hash, mustChange, _, _ := memberProfile(t, f, child)
	if !auth.VerifyPassword("fresh-pass-123", hash) {
		t.Fatal("admin-set password does not verify")
	}
	if mustChange {
		t.Fatal("must_change_password set; admin-set password should not force a change")
	}
}

func TestAdminCannotSetOwnPasswordThroughMemberEdit(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID

	rec := call(t, f.h.UpdateMember, actor, actor, UpdateMemberRequest{Password: strp("fresh-pass-123")})
	expectStatus(t, rec, http.StatusConflict)
	expectErrorCode(t, rec, "use_change_password")
}

func TestEmailMustBeUniqueAcrossMembers(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	if _, err := f.db.ExecContext(context.Background(),
		`UPDATE users SET email = 'parent@example.test' WHERE id = (SELECT user_id FROM family_members WHERE id = ?)`, actor,
	); err != nil {
		t.Fatalf("seed email: %v", err)
	}
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	rec := call(t, f.h.UpdateMember, actor, child, UpdateMemberRequest{Email: strp(" Parent@Example.TEST ")})
	expectStatus(t, rec, http.StatusConflict)
	expectErrorCode(t, rec, "email_taken")
	if got := memberEmail(t, f, child); got.Valid {
		t.Fatalf("child email = %q, want unchanged NULL", got.String)
	}

	rec = call(t, f.h.CreateMember, actor, "", CreateMemberRequest{
		RoleID: customRoleID(t, f, "Grandparent"), UserID: "nana", Password: "nana-pass-123",
		DisplayName: "Nana", Email: "PARENT@example.test",
	})
	expectStatus(t, rec, http.StatusConflict)
	expectErrorCode(t, rec, "email_taken")

	expectStatus(t, call(t, f.h.UpdateMember, actor, actor,
		UpdateMemberRequest{Email: strp("parent@example.test")}), http.StatusNoContent)
}

func TestAddParentRequiresEmail(t *testing.T) {
	f := newFixture(t)

	rec := call(t, f.h.CreateMember, f.fam.MemberID, "", CreateMemberRequest{
		RoleID: parentRoleID, UserID: "dad", Password: "dad-pass-123", DisplayName: "Dad",
	})
	expectStatus(t, rec, http.StatusBadRequest)
	expectErrorCode(t, rec, "validation_failed")

	expectStatus(t, call(t, f.h.CreateMember, f.fam.MemberID, "", CreateMemberRequest{
		RoleID: parentRoleID, UserID: "dad", Password: "dad-pass-123", DisplayName: "Dad", Email: "dad@example.test",
	}), http.StatusCreated)
}

func TestParentSelfProfileRequiresEmail(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID

	rec := call(t, f.h.UpdateOwnProfile, actor, "", UpdateOwnProfileRequest{DisplayName: "Sam", Email: " "})
	expectStatus(t, rec, http.StatusBadRequest)
	expectErrorCode(t, rec, "validation_failed")
}

func TestOwnProfileSavesDetailsForChild(t *testing.T) {
	f := newFixture(t)
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	expectStatus(t, call(t, f.h.UpdateOwnProfile, child, "", UpdateOwnProfileRequest{
		DisplayName: "  Kiddo ", Email: " Kid@Example.TEST ", DateOfBirth: "2015-06-30", Gender: "female",
	}), http.StatusNoContent)

	if got := memberEmail(t, f, child); !got.Valid || got.String != "kid@example.test" {
		t.Fatalf("email = %v, want kid@example.test", got)
	}
	_, _, dob, gender := memberProfile(t, f, child)
	if dob.String != "2015-06-30" || gender.String != "female" {
		t.Fatalf("dob=%v gender=%v", dob, gender)
	}
	var name string
	if err := f.db.QueryRow(
		`SELECT u.display_name FROM users u JOIN family_members fm ON fm.user_id = u.id WHERE fm.id = ?`, child,
	).Scan(&name); err != nil {
		t.Fatalf("load name: %v", err)
	}
	if name != "Kiddo" {
		t.Fatalf("display_name = %q, want Kiddo", name)
	}
}

func TestOwnProfileRejectsEmailOfAnotherMember(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	if _, err := f.db.ExecContext(context.Background(),
		`UPDATE users SET email = 'parent@example.test' WHERE id = (SELECT user_id FROM family_members WHERE id = ?)`, actor,
	); err != nil {
		t.Fatalf("seed email: %v", err)
	}
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	rec := call(t, f.h.UpdateOwnProfile, child, "", UpdateOwnProfileRequest{DisplayName: "Kid", Email: "PARENT@example.test"})
	expectStatus(t, rec, http.StatusConflict)
	expectErrorCode(t, rec, "email_taken")
}

func TestAddMemberStoresProfileFieldsAndListsThem(t *testing.T) {
	f := newFixture(t)
	caregiver := customRoleID(t, f, "Grandparent")

	expectStatus(t, call(t, f.h.CreateMember, f.fam.MemberID, "", CreateMemberRequest{
		RoleID: caregiver, UserID: "nana", Password: "nana-pass-123", DisplayName: "Nana",
		Email: " Nana@Example.TEST", DateOfBirth: "1950-02-14", Gender: "female",
	}), http.StatusCreated)

	rec := call(t, f.h.ListMembers, f.fam.MemberID, "", nil)
	expectStatus(t, rec, http.StatusOK)
	var got ListMembersResponse
	decodeResp(t, rec, &got)
	for _, m := range got.Members {
		if m.UserID != "nana" {
			continue
		}
		if m.Email == nil || *m.Email != "nana@example.test" {
			t.Errorf("email = %v, want nana@example.test", m.Email)
		}
		if m.DateOfBirth == nil || *m.DateOfBirth != "1950-02-14" {
			t.Errorf("date_of_birth = %v, want 1950-02-14", m.DateOfBirth)
		}
		if m.Gender == nil || *m.Gender != "female" {
			t.Errorf("gender = %v, want female", m.Gender)
		}
		return
	}
	t.Fatal("created member not listed")
}
