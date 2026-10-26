package family

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

const (
	maxRoleNameLength        = 40
	maxRoleDescriptionLength = 200
	parentRoleName           = "Parent"
	maxCustomRoles           = 2
)

// Role types. The Parent role is "parent"; custom roles are "child" or "co_parent".
const (
	roleTypeParent   = "parent"
	roleTypeCoParent = "co_parent"
	roleTypeChild    = "child"
)

// builtInRoleIDs are the seeded roles (migration 000001). Every other role is custom.
var builtInRoleIDs = map[string]bool{
	"a1b2c3d4-e5f6-7890-abcd-ef1234567890": true, // Parent
	"c3d4e5f6-a7b8-9012-cdef-123456789012": true, // Child
}

// CatalogueEntry is one permission code in the permissions catalogue.
type CatalogueEntry struct {
	Code        string   `json:"code"`
	Description string   `json:"description"`
	Audience    string   `json:"audience"`
	Levels      []string `json:"levels"`
}

// audienceOf says which role types may hold a code: "any" (child and co-parent roles),
// "co_parent" (co-parent roles only, plus the fixed Parent role) or "parent" (fixed Parent role only).
func audienceOf(code string) string {
	switch {
	case permissions.IsCoParentCode(code):
		return "co_parent"
	case permissions.IsParentOnly(code):
		return "parent"
	default:
		return "any"
	}
}

// CatalogueResponse lists every permission code.
type CatalogueResponse struct {
	Permissions []CatalogueEntry `json:"permissions"`
}

// RoleView is one role with its grants and its default grants.
type RoleView struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	RoleType    string            `json:"role_type"`
	IsFixed     bool              `json:"is_fixed"`
	IsBuiltIn   bool              `json:"is_built_in"`
	Version     int               `json:"version"`
	Permissions map[string]string `json:"permissions"`
	Defaults    map[string]string `json:"defaults"`
}

// ListRolesResponse lists the roles.
type ListRolesResponse struct {
	Roles []RoleView `json:"roles"`
}

// CreateRoleRequest creates a custom role. RoleType is "child" (the default) or "co_parent".
type CreateRoleRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	RoleType    string `json:"role_type"`
}

// CreateRoleResponse returns the new role ID.
type CreateRoleResponse struct {
	ID string `json:"id"`
}

// SetPermissionsRequest replaces the grants of a role or the overrides of a member.
// Codes that are not in the map have no grant.
type SetPermissionsRequest struct {
	Permissions map[string]string `json:"permissions"`
}

// SaveResponse returns the new version after a permissions write.
type SaveResponse struct {
	Version int `json:"version"`
}

// MemberPermissionsResponse returns the per-person overrides for one member.
// Version is also sent as the ETag header and must be echoed in If-Match on writes.
type MemberPermissionsResponse struct {
	IsParent    bool              `json:"is_parent"`
	Version     int               `json:"version"`
	Permissions map[string]string `json:"permissions"`
}

// grantError is a grant that is not allowed. Code is the permission code.
type grantError struct {
	Code   string
	Reason string
}

func (e *grantError) Error() string { return e.Reason }

// PermissionCatalogue handles GET /api/v1/permissions/catalogue.
func (h *Handlers) PermissionCatalogue(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(), `SELECT code, description FROM permissions ORDER BY code`)
	if err != nil {
		h.internalError(w, "failed to list permissions", err)
		return
	}
	defer rows.Close()

	out := []CatalogueEntry{}
	for rows.Next() {
		var e CatalogueEntry
		if err := rows.Scan(&e.Code, &e.Description); err != nil {
			h.internalError(w, "failed to scan permission", err)
			return
		}
		e.Audience = audienceOf(e.Code)
		e.Levels = []string{}
		for _, lvl := range permissions.AllowedLevels(e.Code) {
			e.Levels = append(e.Levels, string(lvl))
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "rows error", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, CatalogueResponse{Permissions: out})
}

// ListRoles handles GET /api/v1/family/roles. It returns each role with its
// grants and its defaults, so the UI can highlight changes.
func (h *Handlers) ListRoles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	roles := []RoleView{}
	index := map[string]int{}
	rows, err := h.db.QueryContext(ctx, `SELECT id, name, description, role_type, is_fixed, version FROM roles ORDER BY is_fixed DESC, name`)
	if err != nil {
		h.internalError(w, "failed to list roles", err)
		return
	}
	for rows.Next() {
		var v RoleView
		if err := rows.Scan(&v.ID, &v.Name, &v.Description, &v.RoleType, &v.IsFixed, &v.Version); err != nil {
			rows.Close()
			h.internalError(w, "failed to scan role", err)
			return
		}
		v.IsBuiltIn = builtInRoleIDs[v.ID]
		v.Permissions = map[string]string{}
		v.Defaults = map[string]string{}
		index[v.ID] = len(roles)
		roles = append(roles, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		h.internalError(w, "rows error", err)
		return
	}

	if err := h.loadGrants(ctx, `SELECT role_id, p.code, access_level FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id`,
		func(roleID, code, level string) {
			if i, ok := index[roleID]; ok {
				roles[i].Permissions[code] = level
			}
		}); err != nil {
		h.internalError(w, "failed to load grants", err)
		return
	}
	if err := h.loadGrants(ctx, `SELECT role_id, p.code, access_level FROM role_default_permissions rd JOIN permissions p ON p.id = rd.permission_id`,
		func(roleID, code, level string) {
			if i, ok := index[roleID]; ok {
				roles[i].Defaults[code] = level
			}
		}); err != nil {
		h.internalError(w, "failed to load defaults", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, ListRolesResponse{Roles: roles})
}

// loadGrants runs a query of (owner_id, code, access_level) rows and passes each to fn.
func (h *Handlers) loadGrants(ctx context.Context, query string, fn func(ownerID, code, level string), args ...any) error {
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var roleID, code, level string
		if err := rows.Scan(&roleID, &code, &level); err != nil {
			return err
		}
		fn(roleID, code, level)
	}
	return rows.Err()
}

// CreateRole handles POST /api/v1/family/roles. Only custom roles can be created.
func (h *Handlers) CreateRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	var req CreateRoleRequest
	if !decodeBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	description := strings.TrimSpace(req.Description)
	roleType := req.RoleType
	if roleType == "" {
		roleType = roleTypeChild
	}
	if roleType != roleTypeChild && roleType != roleTypeCoParent {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Validation failed", map[string]any{"field": "role_type", "reason": "invalid_value"})
		return
	}
	if name == "" || utf8.RuneCountInString(name) > maxRoleNameLength {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Validation failed", map[string]any{"field": "name", "reason": "invalid_length"})
		return
	}
	if utf8.RuneCountInString(description) > maxRoleDescriptionLength {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Validation failed", map[string]any{"field": "description", "reason": "invalid_length"})
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	actorFamilyID, actorUserID, err := actorFamily(ctx, tx, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}
	if roleType == roleTypeCoParent {
		actorIsParent, err := isParentInTx(ctx, tx, actorMemberID)
		if err != nil {
			h.internalError(w, "failed to get actor role", err)
			return
		}
		if !actorIsParent {
			web.RespondError(w, http.StatusForbidden, "parent_only", "Only a parent can create a co-parent role", nil)
			return
		}
	}

	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM roles WHERE LOWER(name) = LOWER(?)`, name).Scan(&taken); err != nil {
		h.internalError(w, "failed to check role name", err)
		return
	}
	if taken > 0 {
		web.RespondError(w, http.StatusConflict, "conflict", "A role with this name already exists", nil)
		return
	}

	customCount, err := countCustomRoles(ctx, tx)
	if err != nil {
		h.internalError(w, "failed to count custom roles", err)
		return
	}
	if customCount >= maxCustomRoles {
		web.RespondError(w, http.StatusConflict, "role_limit", "You can add up to 2 custom roles. Delete one to add another.", map[string]any{"max": maxCustomRoles})
		return
	}

	roleID := ids.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO roles (id, name, description, role_type, is_fixed) VALUES (?, ?, ?, ?, 0)`,
		roleID, name, description, roleType,
	); err != nil {
		h.internalError(w, "failed to create role", err)
		return
	}

	if err := audit.Record(ctx, tx, actorFamilyID, actorUserID, "role.created", "role", &roleID, map[string]any{"name": name, "role_type": roleType}); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}
	web.RespondJSON(w, http.StatusCreated, CreateRoleResponse{ID: roleID})
}

// countCustomRoles counts roles that are not seeded.
func countCustomRoles(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM roles`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		if !builtInRoleIDs[id] {
			count++
		}
	}
	return count, rows.Err()
}

// DeleteRole handles DELETE /api/v1/family/roles/{id}. Only custom roles can be
// deleted, and not while a member still uses the role.
func (h *Handlers) DeleteRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	roleID := r.PathValue("id")
	if !isUUID(roleID) {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid role ID", nil)
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	actorFamilyID, actorUserID, err := actorFamily(ctx, tx, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}

	var name string
	var isFixed bool
	var version int
	err = tx.QueryRowContext(ctx, `SELECT name, is_fixed, version FROM roles WHERE id = ?`, roleID).Scan(&name, &isFixed, &version)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusNotFound, "not_found", "Role not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to get role", err)
		return
	}
	if isFixed {
		web.RespondError(w, http.StatusConflict, "parent_locked", "The parent role cannot be changed", nil)
		return
	}
	if builtInRoleIDs[roleID] {
		web.RespondError(w, http.StatusConflict, "built_in_role", "Built-in roles cannot be deleted", nil)
		return
	}

	if !checkIfMatch(w, r, version) {
		return
	}

	var members int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM family_members WHERE role_id = ?`, roleID).Scan(&members); err != nil {
		h.internalError(w, "failed to count members", err)
		return
	}
	if members > 0 {
		web.RespondError(w, http.StatusConflict, "role_in_use", "This role is still assigned to a member", nil)
		return
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, roleID); err != nil {
		h.internalError(w, "failed to clear grants", err)
		return
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM roles WHERE id = ?`, roleID); err != nil {
		h.internalError(w, "failed to delete role", err)
		return
	}

	if err := audit.Record(ctx, tx, actorFamilyID, actorUserID, "role.deleted", "role", &roleID, map[string]any{"name": name}); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateRolePermissions handles PUT /api/v1/family/roles/{id}/permissions.
// The body replaces all grants of the role. The Parent role is locked.
func (h *Handlers) UpdateRolePermissions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	roleID := r.PathValue("id")
	if !isUUID(roleID) {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid role ID", nil)
		return
	}

	var req SetPermissionsRequest
	if !decodeBody(w, r, &req) {
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	actorFamilyID, actorUserID, err := actorFamily(ctx, tx, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}

	var roleName string
	var isFixed bool
	var version int
	err = tx.QueryRowContext(ctx, `SELECT name, is_fixed, version FROM roles WHERE id = ?`, roleID).Scan(&roleName, &isFixed, &version)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusNotFound, "not_found", "Role not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to get role", err)
		return
	}
	if isFixed {
		web.RespondError(w, http.StatusConflict, "parent_locked", "The parent role cannot be changed", nil)
		return
	}
	if !checkIfMatch(w, r, version) {
		return
	}

	known, err := loadCatalogueIDs(ctx, tx)
	if err != nil {
		h.internalError(w, "failed to load permissions", err)
		return
	}
	roleType, err := roleTypeOf(ctx, tx, roleID)
	if err != nil {
		h.internalError(w, "failed to get role type", err)
		return
	}
	grants, err := parseGrants(req.Permissions, known, roleType)
	if err != nil {
		respondGrantError(w, err)
		return
	}
	actorIsParent, err := isParentInTx(ctx, tx, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor role", err)
		return
	}
	if !actorIsParent {
		changed, err := parentOnlyGrantsChanged(ctx, tx, "role_permissions", "role_id", roleID, grants, known)
		if err != nil {
			h.internalError(w, "failed to compare grants", err)
			return
		}
		if changed {
			web.RespondError(w, http.StatusForbidden, "parent_only", "Only a parent can change parent-only permissions", nil)
			return
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, roleID); err != nil {
		h.internalError(w, "failed to clear grants", err)
		return
	}
	if err := insertGrants(ctx, tx, "role_permissions", "role_id", roleID, grants, known); err != nil {
		h.internalError(w, "failed to save grants", err)
		return
	}
	newVersion := version + 1
	if _, err := tx.ExecContext(ctx, `UPDATE roles SET version = ? WHERE id = ?`, newVersion, roleID); err != nil {
		h.internalError(w, "failed to update role version", err)
		return
	}

	if err := audit.Record(ctx, tx, actorFamilyID, actorUserID, "role.permissions_updated", "role", &roleID,
		map[string]any{"grants": len(grants)}); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, SaveResponse{Version: newVersion})
}

// ResetRolePermissions handles POST /api/v1/family/roles/{id}/reset. It restores
// the default grants of a seeded role. The Parent role is locked.
func (h *Handlers) ResetRolePermissions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	roleID := r.PathValue("id")
	if !isUUID(roleID) {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid role ID", nil)
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	actorFamilyID, actorUserID, err := actorFamily(ctx, tx, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}

	var isFixed bool
	var version int
	err = tx.QueryRowContext(ctx, `SELECT is_fixed, version FROM roles WHERE id = ?`, roleID).Scan(&isFixed, &version)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusNotFound, "not_found", "Role not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to get role", err)
		return
	}
	if isFixed {
		web.RespondError(w, http.StatusConflict, "parent_locked", "The parent role cannot be changed", nil)
		return
	}

	if !checkIfMatch(w, r, version) {
		return
	}

	var defaults int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM role_default_permissions WHERE role_id = ?`, roleID).Scan(&defaults); err != nil {
		h.internalError(w, "failed to count defaults", err)
		return
	}
	if defaults == 0 {
		web.RespondError(w, http.StatusConflict, "no_defaults", "This role has no default permissions", nil)
		return
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, roleID); err != nil {
		h.internalError(w, "failed to clear grants", err)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO role_permissions (role_id, permission_id, access_level)
		 SELECT role_id, permission_id, access_level FROM role_default_permissions WHERE role_id = ?`,
		roleID,
	); err != nil {
		h.internalError(w, "failed to restore defaults", err)
		return
	}

	newVersion := version + 1
	if _, err := tx.ExecContext(ctx, `UPDATE roles SET version = ? WHERE id = ?`, newVersion, roleID); err != nil {
		h.internalError(w, "failed to update role version", err)
		return
	}

	if err := audit.Record(ctx, tx, actorFamilyID, actorUserID, "role.permissions_reset", "role", &roleID, nil); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, SaveResponse{Version: newVersion})
}

// GetMemberPermissions handles GET /api/v1/family/members/{id}/permissions.
func (h *Handlers) GetMemberPermissions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	targetID := r.PathValue("id")
	if !isUUID(targetID) {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid member ID", nil)
		return
	}

	familyID, _, err := actorFamily(ctx, h.db, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}

	var targetFamilyID, roleName string
	var version int
	err = h.db.QueryRowContext(ctx,
		`SELECT fm.family_id, r.name, fm.permissions_version FROM family_members fm JOIN roles r ON r.id = fm.role_id WHERE fm.id = ?`,
		targetID,
	).Scan(&targetFamilyID, &roleName, &version)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && targetFamilyID != familyID) {
		web.RespondError(w, http.StatusNotFound, "not_found", "Member not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to get member", err)
		return
	}

	resp := MemberPermissionsResponse{IsParent: roleName == parentRoleName, Version: version, Permissions: map[string]string{}}
	if err := h.loadGrants(ctx,
		`SELECT mpo.family_member_id, p.code, mpo.access_level
		 FROM member_permission_overrides mpo JOIN permissions p ON p.id = mpo.permission_id
		 WHERE mpo.family_member_id = ?`,
		func(_, code, level string) {
			resp.Permissions[code] = level
		},
		targetID,
	); err != nil {
		h.internalError(w, "failed to load overrides", err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.Itoa(version)+`"`)
	web.RespondJSON(w, http.StatusOK, resp)
}

// UpdateMemberPermissions handles PUT /api/v1/family/members/{id}/permissions.
// The body replaces all overrides of the member. The Parent role is locked.
func (h *Handlers) UpdateMemberPermissions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	targetID := r.PathValue("id")
	if !isUUID(targetID) {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid member ID", nil)
		return
	}

	var req SetPermissionsRequest
	if !decodeBody(w, r, &req) {
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	actorFamilyID, actorUserID, err := actorFamily(ctx, tx, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}

	target, err := lockMember(ctx, tx, targetID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && target.FamilyID != actorFamilyID) {
		web.RespondError(w, http.StatusNotFound, "not_found", "Member not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to get target member", err)
		return
	}
	if target.IsFixed {
		web.RespondError(w, http.StatusConflict, "parent_locked", "Parent permissions cannot be changed", nil)
		return
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT permissions_version FROM family_members WHERE id = ?`, targetID).Scan(&version); err != nil {
		h.internalError(w, "failed to get member version", err)
		return
	}
	if !checkIfMatch(w, r, version) {
		return
	}

	roleType, err := roleTypeOf(ctx, tx, target.RoleID)
	if err != nil {
		h.internalError(w, "failed to get role", err)
		return
	}

	known, err := loadCatalogueIDs(ctx, tx)
	if err != nil {
		h.internalError(w, "failed to load permissions", err)
		return
	}
	grants, err := parseGrants(req.Permissions, known, roleType)
	if err != nil {
		respondGrantError(w, err)
		return
	}

	actorIsParent, err := isParentInTx(ctx, tx, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor role", err)
		return
	}
	if !actorIsParent {
		changed, err := parentOnlyGrantsChanged(ctx, tx, "member_permission_overrides", "family_member_id", targetID, grants, known)
		if err != nil {
			h.internalError(w, "failed to compare overrides", err)
			return
		}
		if changed {
			web.RespondError(w, http.StatusForbidden, "parent_only", "Only a parent can change parent-only permissions", nil)
			return
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM member_permission_overrides WHERE family_member_id = ?`, targetID); err != nil {
		h.internalError(w, "failed to clear overrides", err)
		return
	}
	if err := insertGrants(ctx, tx, "member_permission_overrides", "family_member_id", targetID, grants, known); err != nil {
		h.internalError(w, "failed to save overrides", err)
		return
	}
	newVersion := version + 1
	if _, err := tx.ExecContext(ctx, `UPDATE family_members SET permissions_version = ? WHERE id = ?`, newVersion, targetID); err != nil {
		h.internalError(w, "failed to update member version", err)
		return
	}

	if err := audit.Record(ctx, tx, actorFamilyID, actorUserID, "member.permissions_updated", "family_member", &targetID,
		map[string]any{"overrides": len(grants)}); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, SaveResponse{Version: newVersion})
}

// errInvalidIfMatch means the If-Match header is not a version number.
var errInvalidIfMatch = errors.New("invalid If-Match header")

// parseIfMatch returns the version in the If-Match header, or -1 when the header is absent.
// Versions are quoted, as in an ETag ("3"). A weak prefix (W/) is accepted.
func parseIfMatch(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return -1, nil
	}
	n, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(raw, "W/"), `"`))
	if err != nil || n < 1 {
		return 0, errInvalidIfMatch
	}
	return n, nil
}

// checkIfMatch compares the If-Match header with the current version. It writes a
// 400 or 412 response and returns false when the write must not go ahead. A missing
// header skips the check.
func checkIfMatch(w http.ResponseWriter, r *http.Request, current int) bool {
	expected, err := parseIfMatch(r)
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid If-Match header", nil)
		return false
	}
	if expected >= 0 && expected != current {
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"These permissions changed since you loaded them. Reload and try again.", map[string]any{"version": current})
		return false
	}
	return true
}

// loadCatalogueIDs maps every permission code to its ID.
func loadCatalogueIDs(ctx context.Context, tx *sql.Tx) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT code, id FROM permissions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var code, id string
		if err := rows.Scan(&code, &id); err != nil {
			return nil, err
		}
		out[code] = id
	}
	return out, rows.Err()
}

// parseGrants checks a grant map against the catalogue and the level rules.
// Parent-only codes are accepted only on a co-parent role, and only the codes in
// the co-parent set. It returns a grantError for the first problem found.
func parseGrants(raw map[string]string, known map[string]string, roleType string) (map[string]permissions.AccessLevel, error) {
	out := make(map[string]permissions.AccessLevel, len(raw))
	for code, lvl := range raw {
		if _, ok := known[code]; !ok {
			return nil, &grantError{Code: code, Reason: "unknown_permission"}
		}
		if permissions.IsParentOnly(code) && !(roleType == roleTypeCoParent && permissions.IsCoParentCode(code)) {
			return nil, &grantError{Code: code, Reason: "parent_only"}
		}
		level := permissions.AccessLevel(lvl)
		if !permissions.IsValidLevel(code, level) {
			return nil, &grantError{Code: code, Reason: "invalid_level"}
		}
		out[code] = level
	}
	return out, nil
}

// roleTypeOf returns the role_type of a role.
func roleTypeOf(ctx context.Context, tx *sql.Tx, roleID string) (string, error) {
	var roleType string
	err := tx.QueryRowContext(ctx, `SELECT role_type FROM roles WHERE id = ?`, roleID).Scan(&roleType)
	return roleType, err
}

// isParentInTx reports whether a family member holds the fixed Parent role.
func isParentInTx(ctx context.Context, tx *sql.Tx, memberID string) (bool, error) {
	var isFixed bool
	err := tx.QueryRowContext(ctx,
		`SELECT r.is_fixed FROM family_members fm JOIN roles r ON r.id = fm.role_id
		 WHERE fm.id = ? AND r.name = ?`,
		memberID, parentRoleName,
	).Scan(&isFixed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return isFixed, err
}

// purgeDisallowedOverrides removes a member's parent-only overrides that a role of
// roleType may not hold. It returns the number of rows removed.
func purgeDisallowedOverrides(ctx context.Context, tx *sql.Tx, memberID, roleType string) (int64, error) {
	known, err := loadCatalogueIDs(ctx, tx)
	if err != nil {
		return 0, err
	}
	var deleted int64
	for code, id := range known {
		if !permissions.IsParentOnly(code) || (roleType == roleTypeCoParent && permissions.IsCoParentCode(code)) {
			continue
		}
		res, err := tx.ExecContext(ctx,
			`DELETE FROM member_permission_overrides WHERE family_member_id = ? AND permission_id = ?`, memberID, id)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		deleted += n
	}
	return deleted, nil
}

// parentOnlyGrantsChanged reports whether the new grants differ from the stored rows in
// table (role_permissions or member_permission_overrides) for any parent-only code.
// Parent-only codes missing from grants count as removed.
func parentOnlyGrantsChanged(ctx context.Context, tx *sql.Tx, table, ownerColumn, ownerID string, grants map[string]permissions.AccessLevel, known map[string]string) (bool, error) {
	query := `SELECT p.code, t.access_level FROM ` + table + ` t
		 JOIN permissions p ON p.id = t.permission_id WHERE t.` + ownerColumn + ` = ?`
	rows, err := tx.QueryContext(ctx, query, ownerID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	stored := map[string]permissions.AccessLevel{}
	for rows.Next() {
		var code, level string
		if err := rows.Scan(&code, &level); err != nil {
			return false, err
		}
		stored[code] = permissions.AccessLevel(level)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for code := range known {
		if permissions.IsParentOnly(code) && stored[code] != grants[code] {
			return true, nil
		}
	}
	return false, nil
}

// insertGrants writes one row per grant into table (role_permissions or member_permission_overrides).
func insertGrants(ctx context.Context, tx *sql.Tx, table, ownerColumn, ownerID string, grants map[string]permissions.AccessLevel, codeToID map[string]string) error {
	query := "INSERT INTO " + table + " (" + ownerColumn + ", permission_id, access_level) VALUES (?, ?, ?)"
	for code, level := range grants {
		if _, err := tx.ExecContext(ctx, query, ownerID, codeToID[code], string(level)); err != nil {
			return err
		}
	}
	return nil
}

// respondGrantError writes a grantError as a 400 response, or a generic 400 for other errors.
func respondGrantError(w http.ResponseWriter, err error) {
	var ge *grantError
	if errors.As(err, &ge) {
		web.RespondError(w, http.StatusBadRequest, ge.Reason, "Invalid permission setting",
			map[string]any{"code": ge.Code})
		return
	}
	web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
}
