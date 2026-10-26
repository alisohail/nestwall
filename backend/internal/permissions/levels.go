package permissions

import "strings"

// AllowedLevels returns the access levels that can be set for a permission code.
//
//   - denied and allowed apply to every code.
//   - view applies only to codes ending in .view (read-only).
//   - own applies to the codes in ownScopeCodes (own items only).
//   - request applies to codes where a child asks and a parent approves.
//   - full applies to every code except read-only ones.
//
// Roles and member overrides are checked against this list. Parent-only codes are
// rejected for other roles by the grant handlers, not here.
func AllowedLevels(code string) []AccessLevel {
	levels := []AccessLevel{AccessDenied, AccessAllowed}

	readOnly := strings.HasSuffix(code, ".view")
	if readOnly {
		if ownScopeCodes[code] {
			levels = append(levels, AccessOwn)
		}
		return append(levels, AccessView)
	}

	if ownScopeCodes[code] {
		levels = append(levels, AccessOwn)
	}
	if requestCodes[code] {
		levels = append(levels, AccessRequest)
	}
	return append(levels, AccessFull)
}

// ownScopeCodes are the codes that can be set to own, meaning only the caller's own items.
// Each one is a view code; the handler decides the scope from the level.
var ownScopeCodes = map[string]bool{
	PermTasksView:    true,
	PermCalendarView: true,
	PermRewardsView:  true,
}

// requestCodes are the codes where a child can ask for approval.
var requestCodes = map[string]bool{
	PermTasksComplete: true,
	PermRewardsRedeem: true,
}

// IsValidLevel reports whether level can be set for code.
func IsValidLevel(code string, level AccessLevel) bool {
	for _, allowed := range AllowedLevels(code) {
		if allowed == level {
			return true
		}
	}
	return false
}
