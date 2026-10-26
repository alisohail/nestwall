// Package apitokens issues read-only API tokens for external tools such as Home Assistant.
package apitokens

import (
	"nestwall/backend/internal/permissions"
)

// Scopes a token can carry. Each one maps to the permission the token owner must hold.
const (
	ScopeTasksRead    = "tasks:read"
	ScopeCalendarRead = "calendar:read"
	ScopePointsRead   = "points:read"
	ScopeRewardsRead  = "rewards:read"
)

// scopePermissions maps each scope to the permission that backs it. A token never grants more than
// its owner holds, so both the creation check and the per-request check use this map.
var scopePermissions = map[string]string{
	ScopeTasksRead:    permissions.PermTasksView,
	ScopeCalendarRead: permissions.PermCalendarView,
	ScopePointsRead:   permissions.PermRewardsView,
	ScopeRewardsRead:  permissions.PermRewardsView,
}

// permissionForScope returns the permission behind a scope and whether the scope is known.
func permissionForScope(scope string) (string, bool) {
	perm, ok := scopePermissions[scope]
	return perm, ok
}
