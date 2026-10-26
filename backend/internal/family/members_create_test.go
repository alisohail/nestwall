package family

import (
	"net/http"
	"testing"
)

func memberIsChild(t *testing.T, f fixture, userID string) bool {
	t.Helper()
	var isChild bool
	if err := f.db.QueryRow(
		`SELECT u.is_child FROM users u JOIN family_members fm ON fm.user_id = u.id
		 WHERE fm.family_id = ? AND u.user_id = ?`,
		f.fam.FamilyID, userID,
	).Scan(&isChild); err != nil {
		t.Fatalf("load is_child for %s: %v", userID, err)
	}
	return isChild
}

// customRoleID creates a custom role and returns its ID. Adults with no built-in role use these now.
func customRoleID(t *testing.T, f fixture, name string) string {
	t.Helper()
	expectStatus(t, call(t, f.h.CreateRole, f.fam.MemberID, "", CreateRoleRequest{Name: name}), http.StatusCreated)
	return roleIDByName(t, f.db, name)
}

func TestParentAddsAdultWithAnyRole(t *testing.T) {
	f := newFixture(t)
	caregiver := customRoleID(t, f, "Grandparent")

	rec := call(t, f.h.CreateMember, f.fam.MemberID, "", CreateMemberRequest{
		RoleID: caregiver, UserID: "nana", Password: "nana-pass-123", DisplayName: "Nana",
	})
	expectStatus(t, rec, http.StatusCreated)

	if memberIsChild(t, f, "nana") {
		t.Fatal("caregiver account was marked as a child")
	}
	var roleID string
	if err := f.db.QueryRow(
		`SELECT fm.role_id FROM family_members fm JOIN users u ON u.id = fm.user_id WHERE u.user_id = ?`, "nana",
	).Scan(&roleID); err != nil {
		t.Fatalf("load role: %v", err)
	}
	if roleID != caregiver {
		t.Fatalf("role_id = %s, want %s", roleID, caregiver)
	}
}

func TestParentAddsChildRoleSetsChildFlag(t *testing.T) {
	f := newFixture(t)
	child := roleIDByName(t, f.db, "Child")

	rec := call(t, f.h.CreateMember, f.fam.MemberID, "", CreateMemberRequest{
		RoleID: child, UserID: "ava", Password: "ava-pass-123", DisplayName: "Ava", PIN: "1234",
	})
	expectStatus(t, rec, http.StatusCreated)

	if !memberIsChild(t, f, "ava") {
		t.Fatal("child account was not marked as a child")
	}
}

func TestAddMemberRejectsUnknownRole(t *testing.T) {
	f := newFixture(t)

	for _, roleID := range []string{"not-a-uuid", "11111111-1111-4111-8111-111111111111"} {
		rec := call(t, f.h.CreateMember, f.fam.MemberID, "", CreateMemberRequest{
			RoleID: roleID, UserID: "ghost", Password: "ghost-pass-123", DisplayName: "Ghost",
		})
		expectStatus(t, rec, http.StatusBadRequest)
		expectErrorCode(t, rec, "invalid_role")
	}
}

func TestAddMemberRejectsTakenUsername(t *testing.T) {
	f := newFixture(t)
	caregiver := customRoleID(t, f, "Grandparent")
	body := CreateMemberRequest{RoleID: caregiver, UserID: "nana", Password: "nana-pass-123", DisplayName: "Nana"}

	expectStatus(t, call(t, f.h.CreateMember, f.fam.MemberID, "", body), http.StatusCreated)
	rec := call(t, f.h.CreateMember, f.fam.MemberID, "", body)
	expectStatus(t, rec, http.StatusConflict)
	expectErrorCode(t, rec, "user_id_taken")
}
