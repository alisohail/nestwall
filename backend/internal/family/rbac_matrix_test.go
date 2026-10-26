package family

import (
	"context"
	"net/http"
	"testing"

	"nestwall/backend/internal/permissions"
)

// parentOnlyActions are the parent-only codes from the RBAC milestone.
var parentOnlyActions = []string{
	"family.manage", "members.manage", "roles.manage", "settings.manage",
	"backup.manage", "backup.restore", "data.reset", "audit.view",
	"tasks.approve", "rewards.approve", "bonus.manage",
}

// TestRoleMatrixEffectiveAccess checks the effective access of each role type
// against the rules in design/rbac-milestone.md.
func TestRoleMatrixEffectiveAccess(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	policy := permissions.NewPolicy(f.db)
	parent := f.fam.MemberID

	helperRole := createHelperChildRole(t, f)
	coParentRole := createCoParentRole(t, f)
	expectStatus(t, call(t, f.h.UpdateRolePermissions, parent, coParentRole,
		SetPermissionsRequest{Permissions: map[string]string{"members.manage": "allowed", "tasks.approve": "allowed"}}), http.StatusOK)

	child := addMember(t, f.db, f.fam.FamilyID, "Child")
	helper := addMember(t, f.db, f.fam.FamilyID, "Child")
	expectStatus(t, call(t, f.h.UpdateMember, parent, helper, UpdateMemberRequest{RoleID: strp(helperRole)}), http.StatusNoContent)
	coParent := addMember(t, f.db, f.fam.FamilyID, "Co-parent")

	allowed := func(member, code string) bool {
		t.Helper()
		res, err := policy.Check(ctx, member, code)
		if err != nil {
			t.Fatalf("check %s for %s: %v", code, member, err)
		}
		return res.Allowed
	}

	// Parent holds every parent-only action and task management.
	for _, code := range append([]string{"tasks.manage"}, parentOnlyActions...) {
		if !allowed(parent, code) {
			t.Errorf("parent: %s denied, want allowed", code)
		}
	}

	// Co-parent holds only what it was granted, never the fixed-Parent actions.
	for _, code := range parentOnlyActions {
		want := code == "members.manage" || code == "tasks.approve"
		if got := allowed(coParent, code); got != want {
			t.Errorf("co-parent: %s allowed = %v, want %v", code, got, want)
		}
	}

	// Seeded Child and a custom child-type role hold no parent-only action or task management.
	for name, member := range map[string]string{"child": child, "custom child": helper} {
		for _, code := range append([]string{"tasks.manage"}, parentOnlyActions...) {
			if allowed(member, code) {
				t.Errorf("%s: %s allowed, want denied", name, code)
			}
		}
	}

	// Child members manage their own sessions.
	if !allowed(child, "sessions.manage") {
		t.Errorf("child: sessions.manage denied, want allowed")
	}

	// The custom child role's grants are honoured with their scope.
	res, err := policy.Check(ctx, helper, "tasks.view")
	if err != nil || !res.Allowed || res.AccessLevel != permissions.AccessOwn {
		t.Errorf("custom child tasks.view = %+v, %v; want allowed at own scope", res, err)
	}
	if allowed(helper, "tasks.complete") {
		t.Errorf("custom child tasks.complete allowed, want denied (not granted)")
	}

	// A parent-only code cannot be granted to a child-type role.
	expectStatus(t, call(t, f.h.UpdateRolePermissions, parent, helperRole,
		SetPermissionsRequest{Permissions: map[string]string{"family.manage": "allowed"}}), http.StatusBadRequest)
}

// createHelperChildRole makes a child-type custom role with a few delegable grants.
func createHelperChildRole(t *testing.T, f fixture) string {
	t.Helper()
	rec := call(t, f.h.CreateRole, f.fam.MemberID, "", CreateRoleRequest{Name: "Helper", RoleType: roleTypeChild})
	expectStatus(t, rec, http.StatusCreated)
	var created CreateRoleResponse
	decodeResp(t, rec, &created)
	expectStatus(t, call(t, f.h.UpdateRolePermissions, f.fam.MemberID, created.ID,
		SetPermissionsRequest{Permissions: map[string]string{"tasks.view": "own", "calendar.view": "view"}}), http.StatusOK)
	return created.ID
}
