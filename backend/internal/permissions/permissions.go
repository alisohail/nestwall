package permissions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// AccessLevel represents the level of access a role or member has to a permission.
type AccessLevel string

const (
	AccessFull    AccessLevel = "full"
	AccessView    AccessLevel = "view"
	AccessOwn     AccessLevel = "own"
	AccessRequest AccessLevel = "request"
	AccessAllowed AccessLevel = "allowed"
	AccessDenied  AccessLevel = "denied"
)

// Permission codes - the catalogue of all permissions in the system.
const (
	// Family management
	PermFamilyManage = "family.manage"
	PermFamilyView   = "family.view"

	// Member management
	PermMembersManage = "members.manage"
	PermMembersView   = "members.view"

	// Roles and permissions
	PermRolesManage = "roles.manage"
	PermRolesView   = "roles.view"

	// Tasks
	PermTasksManage   = "tasks.manage"
	PermTasksView     = "tasks.view"
	PermTasksApprove  = "tasks.approve"
	PermTasksComplete = "tasks.complete"

	// Calendar
	PermCalendarManage = "calendar.manage"
	PermCalendarView   = "calendar.view"

	// Rewards
	PermRewardsManage  = "rewards.manage"
	PermRewardsView    = "rewards.view"
	PermRewardsRedeem  = "rewards.redeem"
	PermRewardsApprove = "rewards.approve"
	PermPointsShare    = "points.share"

	// Bonus tasks
	PermBonusManage = "bonus.manage"
	PermBonusGrab   = "bonus.grab"

	// Audit
	PermAuditView = "audit.view"

	// Settings
	PermSettingsManage = "settings.manage"
	PermSettingsView   = "settings.view"
	PermSettingsTheme  = "settings.theme"

	// Profile
	PermProfileAvatarChange = "profile.avatar.change"
	PermProfileManage       = "profile.manage"

	// Sessions
	PermSessionsManage = "sessions.manage"
	PermSessionsView   = "sessions.view"

	// Backup and restore
	PermBackupManage  = "backup.manage"
	PermBackupRestore = "backup.restore"
	PermDataReset     = "data.reset"
)

// parentOnlyCodes are actions that only the fixed Parent role can hold. The role
// and member grant handlers reject them for every other role and member.
var parentOnlyCodes = map[string]bool{
	PermFamilyManage:   true,
	PermMembersManage:  true,
	PermRolesManage:    true,
	PermSettingsManage: true,
	PermBackupManage:   true,
	PermBackupRestore:  true,
	PermDataReset:      true,
	PermAuditView:      true,
	PermTasksApprove:   true,
	PermRewardsApprove: true,
	PermBonusManage:    true,
}

// IsParentOnly reports whether a permission code can only be held by the Parent role.
func IsParentOnly(permissionCode string) bool {
	return parentOnlyCodes[permissionCode]
}

// coParentCodes are the parent-only actions a co-parent role may also hold. The
// rest of parentOnlyCodes stay with the fixed Parent role.
var coParentCodes = map[string]bool{
	PermFamilyManage:   true,
	PermMembersManage:  true,
	PermSettingsManage: true,
	PermAuditView:      true,
	PermTasksApprove:   true,
	PermRewardsApprove: true,
	PermBonusManage:    true,
}

// IsCoParentCode reports whether a co-parent role may hold a parent-only code.
func IsCoParentCode(permissionCode string) bool {
	return coParentCodes[permissionCode]
}

// Policy evaluates permissions for a family member.
type Policy struct {
	db *sql.DB
}

// NewPolicy creates a new Policy instance.
func NewPolicy(db *sql.DB) *Policy {
	return &Policy{db: db}
}

// CheckResult holds the result of a permission check.
type CheckResult struct {
	Allowed     bool
	AccessLevel AccessLevel
}

// Check evaluates whether a family member has permission to perform an action.
// It checks in order: member override -> role grant -> deny by default.
// Database errors are returned to the caller; they are never treated as "no grant".
func (p *Policy) Check(ctx context.Context, familyMemberID string, permissionCode string) (CheckResult, error) {
	override, err := p.getMemberOverride(ctx, familyMemberID, permissionCode)
	if err != nil {
		return CheckResult{}, fmt.Errorf("checking member override: %w", err)
	}
	if override != "" {
		level := AccessLevel(override)
		return CheckResult{Allowed: levelAllows(permissionCode, level), AccessLevel: level}, nil
	}

	roleAccess, err := p.getRolePermission(ctx, familyMemberID, permissionCode)
	if err != nil {
		return CheckResult{}, fmt.Errorf("checking role permission: %w", err)
	}
	if roleAccess != "" {
		level := AccessLevel(roleAccess)
		return CheckResult{Allowed: levelAllows(permissionCode, level), AccessLevel: level}, nil
	}

	return CheckResult{Allowed: false, AccessLevel: AccessDenied}, nil
}

// levelAllows reports whether an access level grants a permission code.
// The "view" level grants read permissions (codes ending in .view) only, so a
// view-only grant does not satisfy a manage permission. Every other level
// except "denied" grants the permission.
func levelAllows(permissionCode string, level AccessLevel) bool {
	switch level {
	case AccessDenied, "":
		return false
	case AccessView:
		return strings.HasSuffix(permissionCode, ".view")
	default:
		return true
	}
}

// getMemberOverride returns the override level for a member, or "" when there is none.
func (p *Policy) getMemberOverride(ctx context.Context, familyMemberID, permissionCode string) (string, error) {
	var accessLevel string
	err := p.db.QueryRowContext(ctx,
		`SELECT mpo.access_level
		 FROM member_permission_overrides mpo
		 JOIN permissions perm ON perm.id = mpo.permission_id
		 WHERE mpo.family_member_id = ? AND perm.code = ?
		 LIMIT 1`,
		familyMemberID, permissionCode,
	).Scan(&accessLevel)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return accessLevel, nil
}

// getRolePermission returns the role's level for a permission, or "" when the role has no grant.
func (p *Policy) getRolePermission(ctx context.Context, familyMemberID, permissionCode string) (string, error) {
	var accessLevel string
	err := p.db.QueryRowContext(ctx,
		`SELECT rp.access_level
		 FROM role_permissions rp
		 JOIN permissions perm ON perm.id = rp.permission_id
		 JOIN family_members fm ON fm.role_id = rp.role_id
		 WHERE fm.id = ? AND perm.code = ?
		 LIMIT 1`,
		familyMemberID, permissionCode,
	).Scan(&accessLevel)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return accessLevel, nil
}

// IsParent checks if a family member has the fixed Parent role.
func (p *Policy) IsParent(ctx context.Context, familyMemberID string) (bool, error) {
	var isFixed bool
	err := p.db.QueryRowContext(ctx,
		`SELECT r.is_fixed
		 FROM family_members fm
		 JOIN roles r ON r.id = fm.role_id
		 WHERE fm.id = ? AND r.name = 'Parent'
		 LIMIT 1`,
		familyMemberID,
	).Scan(&isFixed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return isFixed, nil
}
