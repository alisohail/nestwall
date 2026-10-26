package family

import (
	"fmt"
	"net/http"
	"testing"

	"nestwall/backend/internal/platform/ids"
)

func createCustomRole(t *testing.T, f fixture, name string) string {
	t.Helper()
	rec := call(t, f.h.CreateRole, f.fam.MemberID, "", CreateRoleRequest{Name: name})
	expectStatus(t, rec, http.StatusCreated)
	var created CreateRoleResponse
	decodeResp(t, rec, &created)
	return created.ID
}

func roleExists(t *testing.T, f fixture, roleID string) bool {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM roles WHERE id = ?`, roleID).Scan(&n); err != nil {
		t.Fatalf("count role: %v", err)
	}
	return n > 0
}

func TestListRolesMarksBuiltInRoles(t *testing.T) {
	f := newFixture(t)
	customID := createCustomRole(t, f, "Helper")

	rec := call(t, f.h.ListRoles, f.fam.MemberID, "", nil)
	expectStatus(t, rec, http.StatusOK)
	var resp ListRolesResponse
	decodeResp(t, rec, &resp)

	seen := map[string]RoleView{}
	for _, r := range resp.Roles {
		seen[r.ID] = r
	}
	if !seen[roleIDByName(t, f.db, "Child")].IsBuiltIn {
		t.Error("Child should be built in")
	}
	if seen[customID].IsBuiltIn {
		t.Error("custom role should not be built in")
	}
	if !seen[roleIDByName(t, f.db, parentRoleName)].IsFixed {
		t.Error("Parent should be fixed")
	}
}

func TestCustomRoleLimit(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID

	for i := 1; i <= maxCustomRoles; i++ {
		expectStatus(t, call(t, f.h.CreateRole, actor, "", CreateRoleRequest{Name: fmt.Sprintf("Custom %d", i)}), http.StatusCreated)
	}
	rec := call(t, f.h.CreateRole, actor, "", CreateRoleRequest{Name: "Too many"})
	expectStatus(t, rec, http.StatusConflict)
	expectErrorCode(t, rec, "role_limit")
}

func TestDeleteRoleRules(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID

	t.Run("parent role is locked", func(t *testing.T) {
		rec := call(t, f.h.DeleteRole, actor, roleIDByName(t, f.db, parentRoleName), nil)
		expectStatus(t, rec, http.StatusConflict)
		expectErrorCode(t, rec, "parent_locked")
	})

	t.Run("built-in roles cannot be deleted", func(t *testing.T) {
		for _, name := range []string{"Child"} {
			rec := call(t, f.h.DeleteRole, actor, roleIDByName(t, f.db, name), nil)
			expectStatus(t, rec, http.StatusConflict)
			expectErrorCode(t, rec, "built_in_role")
		}
	})

	t.Run("unknown role is not found", func(t *testing.T) {
		expectStatus(t, call(t, f.h.DeleteRole, actor, ids.New(), nil), http.StatusNotFound)
	})

	t.Run("bad id is rejected", func(t *testing.T) {
		expectStatus(t, call(t, f.h.DeleteRole, actor, "not-a-uuid", nil), http.StatusBadRequest)
	})

	t.Run("role held by a member is in use", func(t *testing.T) {
		roleID := createCustomRole(t, f, "Helper")
		addMember(t, f.db, f.fam.FamilyID, "Helper")
		rec := call(t, f.h.DeleteRole, actor, roleID, nil)
		expectStatus(t, rec, http.StatusConflict)
		expectErrorCode(t, rec, "role_in_use")
		if !roleExists(t, f, roleID) {
			t.Fatal("role was deleted while in use")
		}
	})

	t.Run("unused role is deleted with its grants", func(t *testing.T) {
		roleID := createCustomRole(t, f, "Spare")
		expectStatus(t, call(t, f.h.UpdateRolePermissions, actor, roleID,
			SetPermissionsRequest{Permissions: map[string]string{"members.view": "view"}}), http.StatusOK)
		expectStatus(t, call(t, f.h.DeleteRole, actor, roleID, nil), http.StatusNoContent)
		if roleExists(t, f, roleID) {
			t.Fatal("role still exists after delete")
		}
		var grants int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM role_permissions WHERE role_id = ?`, roleID).Scan(&grants); err != nil {
			t.Fatalf("count grants: %v", err)
		}
		if grants != 0 {
			t.Fatalf("grants left for deleted role = %d, want 0", grants)
		}
	})

	t.Run("stale version is rejected", func(t *testing.T) {
		roleID := createCustomRole(t, f, "Stale")
		rec := callIfMatch(t, f.h.DeleteRole, actor, roleID, "999", nil)
		expectStatus(t, rec, http.StatusPreconditionFailed)
		if !roleExists(t, f, roleID) {
			t.Fatal("role was deleted despite stale version")
		}
	})
}

func TestDeletingARoleFreesASlot(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID

	roleIDs := make([]string, 0, maxCustomRoles)
	for i := 1; i <= maxCustomRoles; i++ {
		roleIDs = append(roleIDs, createCustomRole(t, f, fmt.Sprintf("Slot %d", i)))
	}
	expectStatus(t, call(t, f.h.CreateRole, actor, "", CreateRoleRequest{Name: "Blocked"}), http.StatusConflict)

	expectStatus(t, call(t, f.h.DeleteRole, actor, roleIDs[0], nil), http.StatusNoContent)
	expectStatus(t, call(t, f.h.CreateRole, actor, "", CreateRoleRequest{Name: "Replacement"}), http.StatusCreated)
}
