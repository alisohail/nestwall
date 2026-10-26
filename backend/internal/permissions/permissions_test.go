package permissions

import (
	"context"
	"testing"

	"nestwall/backend/internal/testhelper"
)

func TestAccessLevelConstants(t *testing.T) {
	tests := []struct {
		level    AccessLevel
		expected string
	}{
		{AccessFull, "full"},
		{AccessView, "view"},
		{AccessOwn, "own"},
		{AccessRequest, "request"},
		{AccessAllowed, "allowed"},
		{AccessDenied, "denied"},
	}
	for _, tt := range tests {
		if string(tt.level) != tt.expected {
			t.Errorf("AccessLevel %q, want %q", tt.level, tt.expected)
		}
	}
}

func TestPermissionCodeConstants(t *testing.T) {
	tests := []struct {
		code     string
		expected string
	}{
		{PermFamilyManage, "family.manage"},
		{PermFamilyView, "family.view"},
		{PermMembersManage, "members.manage"},
		{PermMembersView, "members.view"},
		{PermRolesManage, "roles.manage"},
		{PermTasksApprove, "tasks.approve"},
		{PermSettingsManage, "settings.manage"},
		{PermAuditView, "audit.view"},
	}
	for _, tt := range tests {
		if tt.code != tt.expected {
			t.Errorf("permission code %q, want %q", tt.code, tt.expected)
		}
	}
}

// TestLevelAllows covers the access rules used by Check.
// A view grant satisfies read codes only. Denied never grants.
func TestLevelAllows(t *testing.T) {
	tests := []struct {
		name  string
		code  string
		level AccessLevel
		want  bool
	}{
		{"denied never grants a read", PermMembersView, AccessDenied, false},
		{"denied never grants a manage", PermMembersManage, AccessDenied, false},
		{"empty level never grants", PermMembersView, "", false},
		{"view grants a read code", PermMembersView, AccessView, true},
		{"view does not grant a manage code", PermMembersManage, AccessView, false},
		{"full grants a manage code", PermMembersManage, AccessFull, true},
		{"full grants a read code", PermMembersView, AccessFull, true},
		{"own grants a view code at own scope", PermTasksView, AccessOwn, true},
		{"allowed grants a manage code", PermMembersManage, AccessAllowed, true},
		{"request grants a manage code", PermMembersManage, AccessRequest, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := levelAllows(tt.code, tt.level); got != tt.want {
				t.Errorf("levelAllows(%q, %q) = %v, want %v", tt.code, tt.level, got, tt.want)
			}
		})
	}
}

// TestCheckDatabase verifies Check against the seeded Parent role.
// It opens a temporary encrypted SQLite database (requires cgo).
func TestCheckDatabase(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	policy := NewPolicy(db)
	ctx := context.Background()

	res, err := policy.Check(ctx, fam.MemberID, PermMembersManage)
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !res.Allowed {
		t.Errorf("Parent should be allowed %s, got %+v", PermMembersManage, res)
	}

	// An unknown permission code is denied by default, with no error.
	res, err = policy.Check(ctx, fam.MemberID, "no.such.permission")
	if err != nil {
		t.Fatalf("Check for unknown code returned error: %v", err)
	}
	if res.Allowed || res.AccessLevel != AccessDenied {
		t.Errorf("unknown permission must be denied, got %+v", res)
	}

	isParent, err := policy.IsParent(ctx, fam.MemberID)
	if err != nil {
		t.Fatalf("IsParent returned error: %v", err)
	}
	if !isParent {
		t.Error("seeded member should be a fixed Parent")
	}
}
