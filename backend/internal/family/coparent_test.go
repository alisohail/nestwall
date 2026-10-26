package family

import (
	"net/http"
	"testing"
)

// createCoParentRole makes a custom co-parent role named "Co-parent" and returns its ID.
func createCoParentRole(t *testing.T, f fixture) string {
	t.Helper()
	rec := call(t, f.h.CreateRole, f.fam.MemberID, "", CreateRoleRequest{Name: "Co-parent", RoleType: roleTypeCoParent})
	expectStatus(t, rec, http.StatusCreated)
	var created CreateRoleResponse
	decodeResp(t, rec, &created)
	return created.ID
}

func TestCreateRoleRejectsUnknownRoleType(t *testing.T) {
	f := newFixture(t)
	expectStatus(t, call(t, f.h.CreateRole, f.fam.MemberID, "", CreateRoleRequest{Name: "Bad", RoleType: "parent"}), http.StatusBadRequest)
}

func TestCoParentRoleTypeIsListed(t *testing.T) {
	f := newFixture(t)
	coParent := createCoParentRole(t, f)

	rec := call(t, f.h.ListRoles, f.fam.MemberID, "", nil)
	expectStatus(t, rec, http.StatusOK)
	var resp ListRolesResponse
	decodeResp(t, rec, &resp)
	for _, role := range resp.Roles {
		switch role.ID {
		case coParent:
			if role.RoleType != roleTypeCoParent {
				t.Errorf("co-parent role_type = %q", role.RoleType)
			}
		case roleIDByName(t, f.db, "Child"):
			if role.RoleType != roleTypeChild {
				t.Errorf("child role_type = %q", role.RoleType)
			}
		}
	}
}

func TestCoParentRoleCanHoldParentOnlyCodesFromTheCoParentSet(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	coParent := createCoParentRole(t, f)
	child := roleIDByName(t, f.db, "Child")

	// members.manage is in the co-parent set, so it is accepted on a co-parent role only.
	expectStatus(t, call(t, f.h.UpdateRolePermissions, actor, coParent,
		SetPermissionsRequest{Permissions: map[string]string{"members.manage": "allowed"}}), http.StatusOK)
	expectStatus(t, call(t, f.h.UpdateRolePermissions, actor, child,
		SetPermissionsRequest{Permissions: map[string]string{"members.manage": "allowed"}}), http.StatusBadRequest)

	// roles.manage is parent-only but not in the co-parent set, so it is still rejected.
	rec := call(t, f.h.UpdateRolePermissions, actor, coParent,
		SetPermissionsRequest{Permissions: map[string]string{"roles.manage": "allowed"}})
	expectStatus(t, rec, http.StatusBadRequest)
	expectErrorCode(t, rec, "parent_only")
}

func TestCoParentMemberOverridesAndEscalationGuards(t *testing.T) {
	f := newFixture(t)
	parent := f.fam.MemberID
	coParentRole := createCoParentRole(t, f)
	coParent := addMember(t, f.db, f.fam.FamilyID, "Co-parent")
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	// A parent grants a parent-only code to the co-parent member.
	expectStatus(t, call(t, f.h.UpdateMemberPermissions, parent, coParent,
		SetPermissionsRequest{Permissions: map[string]string{"members.manage": "allowed"}}), http.StatusOK)

	// A co-parent actor keeps the same parent-only override: allowed.
	expectStatus(t, call(t, f.h.UpdateMemberPermissions, coParent, child,
		SetPermissionsRequest{Permissions: map[string]string{}}), http.StatusOK)

	// A co-parent actor cannot change a parent-only override, on itself or anyone else.
	rec := call(t, f.h.UpdateMemberPermissions, coParent, coParent, SetPermissionsRequest{Permissions: map[string]string{}})
	expectStatus(t, rec, http.StatusForbidden)
	expectErrorCode(t, rec, "parent_only")

	// A co-parent actor cannot edit a parent account.
	rec = call(t, f.h.UpdateMember, coParent, parent, UpdateMemberRequest{Colour: strp("#10B981")})
	expectStatus(t, rec, http.StatusForbidden)
	expectErrorCode(t, rec, "parent_only")

	// A co-parent actor cannot remove a parent account.
	rec = call(t, f.h.RemoveMember, coParent, parent, nil)
	expectStatus(t, rec, http.StatusForbidden)
	expectErrorCode(t, rec, "parent_only")

	// A co-parent actor cannot assign the co-parent role.
	rec = call(t, f.h.UpdateMember, coParent, child, UpdateMemberRequest{RoleID: strp(coParentRole)})
	expectStatus(t, rec, http.StatusForbidden)
	expectErrorCode(t, rec, "parent_only")

	// A co-parent actor cannot add a parent or a co-parent.
	rec = call(t, f.h.CreateMember, coParent, "", CreateMemberRequest{
		RoleID: parentRoleID, UserID: "dad2", Password: "dad-pass-123", DisplayName: "Dad", Email: "dad2@example.test",
	})
	expectStatus(t, rec, http.StatusForbidden)
	expectErrorCode(t, rec, "parent_only")
	rec = call(t, f.h.CreateMember, coParent, "", CreateMemberRequest{
		RoleID: coParentRole, UserID: "uncle", Password: "uncle-pass-123", DisplayName: "Uncle", Colour: "#10B981",
	})
	expectStatus(t, rec, http.StatusForbidden)
	expectErrorCode(t, rec, "parent_only")

	// A co-parent actor cannot create a co-parent role.
	rec = call(t, f.h.CreateRole, coParent, "", CreateRoleRequest{Name: "Helpers", RoleType: roleTypeCoParent})
	expectStatus(t, rec, http.StatusForbidden)
	expectErrorCode(t, rec, "parent_only")
}

func TestDemotingCoParentDropsParentOnlyOverrides(t *testing.T) {
	f := newFixture(t)
	parent := f.fam.MemberID
	_ = createCoParentRole(t, f)
	coParent := addMember(t, f.db, f.fam.FamilyID, "Co-parent")

	expectStatus(t, call(t, f.h.UpdateMemberPermissions, parent, coParent,
		SetPermissionsRequest{Permissions: map[string]string{"members.manage": "allowed"}}), http.StatusOK)
	if n := countRows(t, f.db, `SELECT COUNT(*) FROM member_permission_overrides WHERE family_member_id = ?`, coParent); n != 1 {
		t.Fatalf("overrides before demotion = %d, want 1", n)
	}

	before := 0
	if err := f.db.QueryRow(`SELECT permissions_version FROM family_members WHERE id = ?`, coParent).Scan(&before); err != nil {
		t.Fatal(err)
	}
	child := roleIDByName(t, f.db, "Child")
	expectStatus(t, call(t, f.h.UpdateMember, parent, coParent, UpdateMemberRequest{RoleID: strp(child)}), http.StatusNoContent)

	if n := countRows(t, f.db, `SELECT COUNT(*) FROM member_permission_overrides WHERE family_member_id = ?`, coParent); n != 0 {
		t.Errorf("overrides after demotion = %d, want 0", n)
	}
	after := 0
	if err := f.db.QueryRow(`SELECT permissions_version FROM family_members WHERE id = ?`, coParent).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Errorf("permissions_version = %d, want greater than %d after purge", after, before)
	}
}

func TestParentOnlyCodesOnChildOverrideStillRejected(t *testing.T) {
	f := newFixture(t)
	child := addMember(t, f.db, f.fam.FamilyID, "Child")
	rec := call(t, f.h.UpdateMemberPermissions, f.fam.MemberID, child,
		SetPermissionsRequest{Permissions: map[string]string{"members.manage": "allowed"}})
	expectStatus(t, rec, http.StatusBadRequest)
	expectErrorCode(t, rec, "parent_only")
}
