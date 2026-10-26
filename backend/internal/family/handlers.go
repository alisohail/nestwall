package family

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/avatars"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

const (
	maxJSONBody         = 64 << 10
	defaultMemberColour = "#888888"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// validMemberStatuses lists the statuses a member may be set to via update.
var validMemberStatuses = map[string]bool{"active": true, "inactive": true, "removed": true}

// Handlers handles family and member endpoints.
type Handlers struct {
	db        *sql.DB
	clock     clock.Clock
	logger    *slog.Logger
	avatarDir string
}

// NewHandlers creates a new Handlers instance. avatarDir is the folder that holds picture files.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger, avatarDir string) *Handlers {
	return &Handlers{
		db:        db,
		clock:     clk,
		logger:    logger,
		avatarDir: avatarDir,
	}
}

// ListMembersResponse represents the response for listing members.
type ListMembersResponse struct {
	Members []MemberDetail `json:"members"`
}

// MemberDetail represents a member in the list.
type MemberDetail struct {
	ID          string  `json:"id"`
	UserID      string  `json:"user_id"`
	DisplayName string  `json:"display_name"`
	RoleID      string  `json:"role_id"`
	RoleName    string  `json:"role_name"`
	Status      string  `json:"status"`
	Colour      string  `json:"colour"`
	IsChild     bool    `json:"is_child"`
	AvatarID    *string `json:"avatar_id"`
	Email       *string `json:"email"`
	DateOfBirth *string `json:"date_of_birth"`
	Gender      *string `json:"gender"`
}

// UpdateMemberRequest represents the request to update a member.
// Email, date of birth and gender are cleared by sending an empty string.
type UpdateMemberRequest struct {
	RoleID      *string `json:"role_id,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Colour      *string `json:"colour,omitempty"`
	Status      *string `json:"status,omitempty"`
	UserID      *string `json:"user_id,omitempty"`
	PIN         *string `json:"pin,omitempty"`
	Email       *string `json:"email,omitempty"`
	DateOfBirth *string `json:"date_of_birth,omitempty"`
	Gender      *string `json:"gender,omitempty"`
	Password    *string `json:"password,omitempty"`
}

// CreateMemberRequest represents the request to add a family member with any role.
type CreateMemberRequest struct {
	RoleID      string `json:"role_id"`
	UserID      string `json:"user_id"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Colour      string `json:"colour"`
	PIN         string `json:"pin,omitempty"`
	Email       string `json:"email,omitempty"`
	DateOfBirth string `json:"date_of_birth,omitempty"`
	Gender      string `json:"gender,omitempty"`
}

// querier is implemented by *sql.DB and *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// memberRecord is a family member row used for guard checks.
type memberRecord struct {
	ID       string
	FamilyID string
	UserID   string
	RoleID   string
	IsFixed  bool
	Status   string
}

// ListMembers handles GET /api/v1/family/members
func (h *Handlers) ListMembers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	familyMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	familyID, _, err := actorFamily(ctx, h.db, familyMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}

	// Inactive members are listed only when the Family page asks for them, so pickers keep seeing active members.
	includeInactive := r.URL.Query().Get("include_inactive") == "true"
	rows, err := h.db.QueryContext(ctx,
		`SELECT fm.id, u.user_id, u.display_name, r.id, r.name, fm.status, fm.colour, u.is_child, u.email,
		        u.date_of_birth, u.gender,
		        (SELECT ma.id FROM member_avatars ma WHERE ma.family_member_id = fm.id LIMIT 1)
		 FROM family_members fm
		 JOIN users u ON fm.user_id = u.id
		 JOIN roles r ON fm.role_id = r.id
		 WHERE fm.family_id = ? AND (fm.status = 'active' OR (? AND fm.status = 'inactive'))
		 ORDER BY r.name, u.display_name`,
		familyID, includeInactive,
	)
	if err != nil {
		h.internalError(w, "failed to list members", err)
		return
	}
	defer rows.Close()

	members := []MemberDetail{}
	for rows.Next() {
		var m MemberDetail
		var avatarID, email, dateOfBirth, gender sql.NullString
		if err := rows.Scan(&m.ID, &m.UserID, &m.DisplayName, &m.RoleID, &m.RoleName, &m.Status, &m.Colour, &m.IsChild, &email, &dateOfBirth, &gender, &avatarID); err != nil {
			h.internalError(w, "failed to scan member", err)
			return
		}
		if avatarID.Valid {
			m.AvatarID = &avatarID.String
		}
		if email.Valid {
			m.Email = &email.String
		}
		if dateOfBirth.Valid {
			m.DateOfBirth = &dateOfBirth.String
		}
		if gender.Valid {
			m.Gender = &gender.String
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "rows error", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, ListMembersResponse{Members: members})
}

// UpdateMember handles PUT /api/v1/family/members/{id}
func (h *Handlers) UpdateMember(w http.ResponseWriter, r *http.Request) {
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

	var req UpdateMemberRequest
	if !decodeBody(w, r, &req) {
		return
	}
	now := h.clock.Now()
	if err := validateUpdate(req, now); err != nil {
		respondInvalid(w, err)
		return
	}

	var passwordHash *string
	if req.Password != nil {
		hashed, err := auth.HashPassword(*req.Password)
		if err != nil {
			h.internalError(w, "failed to hash password", err)
			return
		}
		passwordHash = &hashed
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

	actorIsParent, err := isParentInTx(ctx, tx, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor role", err)
		return
	}
	if !actorIsParent && target.IsFixed {
		web.RespondError(w, http.StatusForbidden, "parent_only", "Only a parent can change a parent account", nil)
		return
	}

	var newRoleType string
	if req.RoleID != nil {
		var newRoleFixed bool
		err := tx.QueryRowContext(ctx, `SELECT is_fixed FROM roles WHERE id = ?`, *req.RoleID).Scan(&newRoleFixed)
		if errors.Is(err, sql.ErrNoRows) || newRoleFixed {
			web.RespondError(w, http.StatusBadRequest, "invalid_role", "Invalid role", nil)
			return
		}
		if err != nil {
			h.internalError(w, "failed to get new role", err)
			return
		}
		if target.IsFixed && *req.RoleID != target.RoleID {
			web.RespondError(w, http.StatusConflict, "conflict", "The parent role cannot be changed", nil)
			return
		}
		if *req.RoleID != target.RoleID {
			newRoleType, err = roleTypeOf(ctx, tx, *req.RoleID)
			if err != nil {
				h.internalError(w, "failed to get new role type", err)
				return
			}
			oldRoleType, err := roleTypeOf(ctx, tx, target.RoleID)
			if err != nil {
				h.internalError(w, "failed to get current role type", err)
				return
			}
			if !actorIsParent && (newRoleType == roleTypeCoParent || oldRoleType == roleTypeCoParent) {
				web.RespondError(w, http.StatusForbidden, "parent_only", "Only a parent can assign or change a co-parent role", nil)
				return
			}
		}
	}

	// Every parent keeps an email address, which is used for password recovery.
	if target.IsFixed && req.Email != nil && strings.TrimSpace(*req.Email) == "" {
		respondInvalid(w, &auth.ValidationError{Field: "email", Reason: "required"})
		return
	}
	// An admin changes their own password through the change-password flow, which keeps the current session.
	if req.Password != nil && targetID == actorMemberID {
		web.RespondError(w, http.StatusConflict, "use_change_password", "Change your own password from Settings", nil)
		return
	}

	deactivating := req.Status != nil && *req.Status != "active" && target.Status == "active"
	if deactivating && targetID == actorMemberID {
		web.RespondError(w, http.StatusConflict, "cannot_deactivate_self", "You cannot deactivate your own account", nil)
		return
	}
	if target.IsFixed && deactivating {
		others, err := countOtherActiveParents(ctx, tx, actorFamilyID, targetID)
		if err != nil {
			h.internalError(w, "failed to count parents", err)
			return
		}
		if others == 0 {
			web.RespondError(w, http.StatusConflict, "last_parent", "Cannot deactivate the last parent", nil)
			return
		}
	}

	if req.UserID != nil {
		taken, err := userIDTaken(ctx, tx, *req.UserID, target.UserID)
		if err != nil {
			h.internalError(w, "failed to check user_id uniqueness", err)
			return
		}
		if taken {
			web.RespondError(w, http.StatusConflict, "user_id_taken", "User ID already exists", nil)
			return
		}
	}

	email := ""
	if req.Email != nil {
		email = strings.ToLower(strings.TrimSpace(*req.Email))
		if email != "" {
			taken, err := emailTaken(ctx, tx, email, target.UserID)
			if err != nil {
				h.internalError(w, "failed to check email uniqueness", err)
				return
			}
			if taken {
				web.RespondError(w, http.StatusConflict, "email_taken", "Email is already used by another member", nil)
				return
			}
		}
	}

	var pinHash *string
	if req.PIN != nil {
		var isChild bool
		if err := tx.QueryRowContext(ctx, `SELECT is_child FROM users WHERE id = ?`, target.UserID).Scan(&isChild); err != nil {
			h.internalError(w, "failed to get member account", err)
			return
		}
		if !isChild {
			web.RespondError(w, http.StatusBadRequest, "invalid_target", "A PIN can only be set for child accounts", nil)
			return
		}
		hashed, err := auth.HashPassword(*req.PIN)
		if err != nil {
			h.internalError(w, "failed to hash pin", err)
			return
		}
		pinHash = &hashed
	}

	sets := []string{}
	args := []any{}
	addSet := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, column+" = ?")
	}
	if req.RoleID != nil {
		addSet("role_id", *req.RoleID)
		if *req.RoleID != target.RoleID {
			deleted, err := purgeDisallowedOverrides(ctx, tx, targetID, newRoleType)
			if err != nil {
				h.internalError(w, "failed to clear overrides", err)
				return
			}
			if deleted > 0 {
				sets = append(sets, "permissions_version = permissions_version + 1")
			}
		}
	}
	if req.Colour != nil {
		addSet("colour", *req.Colour)
	}
	if req.Status != nil {
		addSet("status", *req.Status)
	}
	if pinHash != nil {
		addSet("pin_hash", *pinHash)
	}
	if len(sets) > 0 {
		args = append(args, h.clock.Now(), targetID)
		query := fmt.Sprintf("UPDATE family_members SET %s, updated_at = ? WHERE id = ?", strings.Join(sets, ", "))
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			h.internalError(w, "failed to update member", err)
			return
		}
	}

	if req.DisplayName != nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET display_name = ?, updated_at = ? WHERE id = ?`,
			strings.TrimSpace(*req.DisplayName), now, target.UserID,
		); err != nil {
			h.internalError(w, "failed to update user display name", err)
			return
		}
	}

	if req.UserID != nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET user_id = ?, updated_at = ? WHERE id = ?`,
			*req.UserID, now, target.UserID,
		); err != nil {
			if auth.IsUniqueViolation(err) {
				web.RespondError(w, http.StatusConflict, "user_id_taken", "User ID already exists", nil)
				return
			}
			h.internalError(w, "failed to update user_id", err)
			return
		}
	}

	profileSets := []string{}
	profileArgs := []any{}
	if req.Email != nil {
		profileSets = append(profileSets, "email = ?")
		profileArgs = append(profileArgs, optionalText(email))
	}
	if req.DateOfBirth != nil {
		profileSets = append(profileSets, "date_of_birth = ?")
		profileArgs = append(profileArgs, optionalText(*req.DateOfBirth))
	}
	if req.Gender != nil {
		profileSets = append(profileSets, "gender = ?")
		profileArgs = append(profileArgs, optionalText(*req.Gender))
	}
	// An admin sets the password directly. The member is not forced to change it at next sign-in.
	if passwordHash != nil {
		profileSets = append(profileSets, "password_hash = ?", "must_change_password = 0")
		profileArgs = append(profileArgs, *passwordHash)
	}
	if len(profileSets) > 0 {
		profileArgs = append(profileArgs, now, target.UserID)
		query := fmt.Sprintf("UPDATE users SET %s, updated_at = ? WHERE id = ?", strings.Join(profileSets, ", "))
		if _, err := tx.ExecContext(ctx, query, profileArgs...); err != nil {
			if isEmailUniqueViolation(err) {
				web.RespondError(w, http.StatusConflict, "email_taken", "Email is already used by another member", nil)
				return
			}
			h.internalError(w, "failed to update member profile", err)
			return
		}
	}

	if (req.Status != nil && *req.Status != "active") || passwordHash != nil {
		if err := revokeUserSessions(ctx, tx, now, target.UserID); err != nil {
			h.internalError(w, "failed to revoke sessions", err)
			return
		}
	}

	if req.RoleID != nil || req.Status != nil || req.UserID != nil || req.PIN != nil ||
		req.Email != nil || req.DateOfBirth != nil || req.Gender != nil || req.Password != nil {
		meta := map[string]any{}
		if req.RoleID != nil {
			meta["new_role_id"] = *req.RoleID
		}
		if req.Status != nil {
			meta["new_status"] = *req.Status
		}
		// Credentials and personal details are recorded as a flag only, never as values.
		if req.UserID != nil {
			meta["user_id_changed"] = true
		}
		if req.PIN != nil {
			meta["pin_changed"] = true
		}
		if req.Password != nil {
			meta["password_changed"] = true
		}
		if req.Email != nil {
			meta["email_changed"] = true
		}
		if req.DateOfBirth != nil {
			meta["date_of_birth_changed"] = true
		}
		if req.Gender != nil {
			meta["gender_changed"] = true
		}
		if err := audit.Record(ctx, tx, actorFamilyID, actorUserID, "member.updated", "family_member", &targetID, meta); err != nil {
			h.internalError(w, "failed to record audit log", err)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// UpdateOwnProfileRequest is the caller's own details. Every field is sent on each save.
type UpdateOwnProfileRequest struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	DateOfBirth string `json:"date_of_birth"`
	Gender      string `json:"gender"`
}

// UpdateOwnProfile handles PUT /api/v1/me/profile.
// Any signed-in family member can change their own name, email, date of birth and gender.
// Parents must keep an email address, as on the admin edit page.
func (h *Handlers) UpdateOwnProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	var req UpdateOwnProfileRequest
	if !decodeBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.DisplayName)
	email := strings.ToLower(strings.TrimSpace(req.Email))
	dob := strings.TrimSpace(req.DateOfBirth)
	gender := strings.TrimSpace(req.Gender)
	now := h.clock.Now()
	if err := validateUpdate(UpdateMemberRequest{DisplayName: &name, Email: &email, DateOfBirth: &dob, Gender: &gender}, now); err != nil {
		respondInvalid(w, err)
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	familyID, userID, err := actorFamily(ctx, tx, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}
	me, err := lockMember(ctx, tx, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get member", err)
		return
	}

	if me.IsFixed && email == "" {
		respondInvalid(w, &auth.ValidationError{Field: "email", Reason: "required"})
		return
	}
	if email != "" {
		taken, err := emailTaken(ctx, tx, email, userID)
		if err != nil {
			h.internalError(w, "failed to check email uniqueness", err)
			return
		}
		if taken {
			web.RespondError(w, http.StatusConflict, "email_taken", "Email is already used by another member", nil)
			return
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET display_name = ?, email = ?, date_of_birth = ?, gender = ?, updated_at = ? WHERE id = ?`,
		name, optionalText(email), optionalText(dob), optionalText(gender), now, userID,
	); err != nil {
		if isEmailUniqueViolation(err) {
			web.RespondError(w, http.StatusConflict, "email_taken", "Email is already used by another member", nil)
			return
		}
		h.internalError(w, "failed to update profile", err)
		return
	}

	if err := audit.Record(ctx, tx, familyID, userID, "profile.updated", "user", &userID, nil); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RemoveMember handles DELETE /api/v1/family/members/{id}
// Permission (members.manage) is enforced by the route middleware.
func (h *Handlers) RemoveMember(w http.ResponseWriter, r *http.Request) {
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
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (target.FamilyID != actorFamilyID || target.Status == "removed")) {
		web.RespondError(w, http.StatusNotFound, "not_found", "Member not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to get target member", err)
		return
	}

	if target.ID == actorMemberID {
		web.RespondError(w, http.StatusConflict, "cannot_remove_self", "You cannot remove your own account", nil)
		return
	}

	if target.IsFixed {
		actorIsParent, err := isParentInTx(ctx, tx, actorMemberID)
		if err != nil {
			h.internalError(w, "failed to get actor role", err)
			return
		}
		if !actorIsParent {
			web.RespondError(w, http.StatusForbidden, "parent_only", "Only a parent can remove a parent account", nil)
			return
		}
	}

	if target.IsFixed && target.Status == "active" {
		others, err := countOtherActiveParents(ctx, tx, actorFamilyID, targetID)
		if err != nil {
			h.internalError(w, "failed to count parents", err)
			return
		}
		if others == 0 {
			web.RespondError(w, http.StatusConflict, "conflict", "Cannot remove the last parent", nil)
			return
		}
	}

	avatarIDs, err := memberAvatarIDs(ctx, tx, targetID)
	if err != nil {
		h.internalError(w, "failed to read member pictures", err)
		return
	}

	if err := purgeMemberRecords(ctx, tx, actorFamilyID, targetID); err != nil {
		h.internalError(w, "failed to purge member records", err)
		return
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM family_members WHERE id = ? AND family_id = ?`,
		targetID, actorFamilyID,
	); err != nil {
		h.internalError(w, "failed to delete member", err)
		return
	}

	if err := revokeUserSessions(ctx, tx, h.clock.Now(), target.UserID); err != nil {
		h.internalError(w, "failed to revoke sessions", err)
		return
	}

	if err := audit.Record(ctx, tx, actorFamilyID, actorUserID, "member.removed", "family_member", &targetID, nil); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	// The member_avatars rows were removed with the member; the picture files are removed here.
	avatars.RemoveFiles(h.avatarDir, h.logger, avatarIDs...)

	w.WriteHeader(http.StatusNoContent)
}

// CreateMember handles POST /api/v1/family/members. A parent adds a member
// with any role. The account is a child account only when the role is Child.
// Permission (members.manage) is enforced by the route middleware.
func (h *Handlers) CreateMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	var req CreateMemberRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !isUUID(req.RoleID) {
		web.RespondError(w, http.StatusBadRequest, "invalid_role", "Invalid role", nil)
		return
	}

	if err := auth.ValidateNewUser(req.UserID, req.Password, req.DisplayName); err != nil {
		respondInvalid(w, err)
		return
	}
	colour := req.Colour
	if colour == "" {
		colour = defaultMemberColour
	} else if err := auth.ValidateColour(colour); err != nil {
		respondInvalid(w, err)
		return
	}
	if req.PIN != "" {
		if err := auth.ValidatePIN(req.PIN); err != nil {
			respondInvalid(w, err)
			return
		}
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email != "" {
		if err := auth.ValidateEmail(email); err != nil {
			respondInvalid(w, err)
			return
		}
	}
	dateOfBirth := strings.TrimSpace(req.DateOfBirth)
	if dateOfBirth != "" {
		if err := auth.ValidateBirthDate(dateOfBirth, h.clock.Now()); err != nil {
			respondInvalid(w, err)
			return
		}
	}
	gender := strings.TrimSpace(req.Gender)
	if gender != "" {
		if err := auth.ValidateGender(gender); err != nil {
			respondInvalid(w, err)
			return
		}
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.internalError(w, "failed to hash password", err)
		return
	}

	if err != nil {
		h.internalError(w, "failed to hash password", err)
		return
	}
	var pinHash *string
	if req.PIN != "" {
		hashed, err := auth.HashPassword(req.PIN)
		if err != nil {
			h.internalError(w, "failed to hash pin", err)
			return
		}
		pinHash = &hashed
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

	taken, err := userIDTaken(ctx, tx, req.UserID, "")
	if err != nil {
		h.internalError(w, "failed to check user_id uniqueness", err)
		return
	}
	if taken {
		web.RespondError(w, http.StatusConflict, "user_id_taken", "User ID already exists", nil)
		return
	}
	if email != "" {
		emailInUse, err := emailTaken(ctx, tx, email, "")
		if err != nil {
			h.internalError(w, "failed to check email uniqueness", err)
			return
		}
		if emailInUse {
			web.RespondError(w, http.StatusConflict, "email_taken", "Email is already used by another member", nil)
			return
		}
	}

	var roleName string
	var roleFixed bool
	err = tx.QueryRowContext(ctx, `SELECT name, is_fixed FROM roles WHERE id = ?`, req.RoleID).Scan(&roleName, &roleFixed)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusBadRequest, "invalid_role", "Invalid role", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to get role", err)
		return
	}
	roleType, err := roleTypeOf(ctx, tx, req.RoleID)
	if err != nil {
		h.internalError(w, "failed to get role type", err)
		return
	}
	if roleFixed || roleType == roleTypeCoParent {
		actorIsParent, err := isParentInTx(ctx, tx, actorMemberID)
		if err != nil {
			h.internalError(w, "failed to get actor role", err)
			return
		}
		if !actorIsParent {
			web.RespondError(w, http.StatusForbidden, "parent_only", "Only a parent can add a parent or co-parent", nil)
			return
		}
	}
	// Every parent needs an email address for password recovery.
	if roleFixed && email == "" {
		respondInvalid(w, &auth.ValidationError{Field: "email", Reason: "required"})
		return
	}
	isChild := roleName == "Child"

	userID := ids.New()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child, email, date_of_birth, gender)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, req.UserID, passwordHash, strings.TrimSpace(req.DisplayName), isChild,
		optionalText(email), optionalText(dateOfBirth), optionalText(gender),
	)
	if err != nil {
		if isEmailUniqueViolation(err) {
			web.RespondError(w, http.StatusConflict, "email_taken", "Email is already used by another member", nil)
			return
		}
		if auth.IsUniqueViolation(err) {
			web.RespondError(w, http.StatusConflict, "user_id_taken", "User ID already exists", nil)
			return
		}
		h.internalError(w, "failed to create user", err)
		return
	}

	memberID := ids.New()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour, pin_hash)
		 VALUES (?, ?, ?, ?, 'active', ?, ?)`,
		memberID, actorFamilyID, userID, req.RoleID, colour, pinHash,
	)
	if err != nil {
		h.internalError(w, "failed to create family member", err)
		return
	}

	meta := map[string]any{"role_id": req.RoleID, "is_child": isChild}
	if err := audit.Record(ctx, tx, actorFamilyID, actorUserID, "member.added", "family_member", &memberID, meta); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

// internalError logs the error and writes a generic 500 response.
func (h *Handlers) internalError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
}

// actorFamily returns the family ID and user ID of the calling family member.
func actorFamily(ctx context.Context, q querier, familyMemberID string) (string, string, error) {
	var familyID, userID string
	err := q.QueryRowContext(ctx,
		`SELECT family_id, user_id FROM family_members WHERE id = ?`,
		familyMemberID,
	).Scan(&familyID, &userID)
	return familyID, userID, err
}

// lockMember loads a family member. Write transactions already hold the
// database write lock (BEGIN IMMEDIATE), so no row lock is needed.
func lockMember(ctx context.Context, tx *sql.Tx, memberID string) (*memberRecord, error) {
	var m memberRecord
	err := tx.QueryRowContext(ctx,
		`SELECT fm.id, fm.family_id, fm.user_id, fm.role_id, r.is_fixed, fm.status
		 FROM family_members fm
		 JOIN roles r ON r.id = fm.role_id
		 WHERE fm.id = ?`,
		memberID,
	).Scan(&m.ID, &m.FamilyID, &m.UserID, &m.RoleID, &m.IsFixed, &m.Status)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// countOtherActiveParents counts active members with a fixed (Parent) role, excluding one member.
func countOtherActiveParents(ctx context.Context, tx *sql.Tx, familyID, memberID string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM family_members fm
		 JOIN roles r ON r.id = fm.role_id
		 WHERE fm.family_id = ? AND fm.id <> ? AND fm.status = 'active' AND r.is_fixed = 1`,
		familyID, memberID,
	).Scan(&n)
	return n, err
}

// revokeUserSessions revokes all active sessions for a user at the given time.
func revokeUserSessions(ctx context.Context, tx *sql.Tx, now time.Time, userID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		now, userID,
	)
	return err
}

// memberAvatarIDs returns the IDs of the picture files a member owns, so they can be removed with the member.
func memberAvatarIDs(ctx context.Context, tx *sql.Tx, memberID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM member_avatars WHERE family_member_id = ?`, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// userIDTaken reports whether a user ID is already used by another account (case-insensitive).
// exceptUserID is the account being renamed, so it does not conflict with itself; pass "" when creating.
func userIDTaken(ctx context.Context, tx *sql.Tx, userID, exceptUserID string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE LOWER(user_id) = LOWER(?) AND id <> ?`,
		userID, exceptUserID,
	).Scan(&n)
	return n > 0, err
}

// emailTaken reports whether an email address is already used by another account (case-insensitive).
// exceptUserID is the account being updated, so keeping its own email is not a conflict; pass "" when creating.
func emailTaken(ctx context.Context, tx *sql.Tx, email, exceptUserID string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE LOWER(email) = LOWER(?) AND id <> ?`,
		email, exceptUserID,
	).Scan(&n)
	return n > 0, err
}

// isEmailUniqueViolation reports whether err came from the unique email index (a concurrent insert or update).
func isEmailUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "users_email_unique")
}

// isUUID reports whether s is a canonical UUID string.
func isUUID(s string) bool {
	return uuidPattern.MatchString(s)
}

// decodeBody decodes a bounded JSON body. It writes a 400 response and returns false on failure.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(v); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return false
	}
	return true
}

// validateUpdate checks an update request before any database work.
func validateUpdate(req UpdateMemberRequest, today time.Time) error {
	if req.RoleID == nil && req.DisplayName == nil && req.Colour == nil && req.Status == nil && req.UserID == nil &&
		req.PIN == nil && req.Email == nil && req.DateOfBirth == nil && req.Gender == nil && req.Password == nil {
		return &auth.ValidationError{Field: "body", Reason: "empty"}
	}
	if req.DisplayName != nil {
		if err := auth.ValidateName("display_name", *req.DisplayName); err != nil {
			return err
		}
	}
	if req.Colour != nil {
		if err := auth.ValidateColour(*req.Colour); err != nil {
			return err
		}
	}
	if req.Status != nil && !validMemberStatuses[*req.Status] {
		return &auth.ValidationError{Field: "status", Reason: "invalid_value"}
	}
	if req.UserID != nil {
		if err := auth.ValidateUserID(*req.UserID); err != nil {
			return err
		}
	}
	if req.PIN != nil {
		if err := auth.ValidatePIN(*req.PIN); err != nil {
			return err
		}
	}
	if req.Email != nil {
		if email := strings.ToLower(strings.TrimSpace(*req.Email)); email != "" {
			if err := auth.ValidateEmail(email); err != nil {
				return err
			}
		}
	}
	if req.DateOfBirth != nil {
		if dob := strings.TrimSpace(*req.DateOfBirth); dob != "" {
			if err := auth.ValidateBirthDate(dob, today); err != nil {
				return err
			}
		}
	}
	if req.Gender != nil {
		if gender := strings.TrimSpace(*req.Gender); gender != "" {
			if err := auth.ValidateGender(gender); err != nil {
				return err
			}
		}
	}
	if req.Password != nil {
		if err := auth.ValidatePassword(*req.Password); err != nil {
			return err
		}
	}
	return nil
}

// optionalText trims a value and returns nil when it is empty, so the column is stored as NULL.
func optionalText(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

// respondInvalid writes a 400 validation_failed response.
func respondInvalid(w http.ResponseWriter, err error) {
	var vErr *auth.ValidationError
	if errors.As(err, &vErr) {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input", vErr.Params())
		return
	}
	web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input", nil)
}
