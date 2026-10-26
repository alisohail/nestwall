package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

// ChangePasswordRequest is the body of POST /api/v1/auth/change-password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword handles POST /api/v1/auth/change-password.
//
// It is the only API route a user with must_change_password can use, so it
// does not go through the permission middleware. It needs a valid session and
// the current password. It clears the must-change flag and signs out every
// other session of the user. The current session stays signed in.
func (h *Handlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)

	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}

	ctx := r.Context()
	sess, err := ValidateSession(ctx, h.db, h.clock, cookie.Value)
	if err != nil {
		web.RespondError(w, http.StatusUnauthorized, "session_invalid", "Session is invalid or expired", nil)
		return
	}

	var req ChangePasswordRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&req); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return
	}
	if req.CurrentPassword == "" {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "current_password is required", nil)
		return
	}
	if err := ValidatePassword(req.NewPassword); err != nil {
		respondValidation(w, err)
		return
	}
	if req.NewPassword == req.CurrentPassword {
		web.RespondError(w, http.StatusBadRequest, "same_password", "The new password must be different", nil)
		return
	}

	// Brute-force guard for the current password, shared with login.
	if err := h.limiter.CheckRateLimit(ip, sess.User.UserID); err != nil {
		web.RespondError(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again later.", nil)
		return
	}

	var passwordHash string
	if err := h.db.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE id = ?`, sess.User.ID,
	).Scan(&passwordHash); err != nil {
		h.logger.Error("change password: read hash", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	if !VerifyPassword(req.CurrentPassword, passwordHash) {
		h.limiter.RecordFailure(ip, sess.User.UserID)
		web.RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Current password is wrong", nil)
		return
	}

	newHash, err := HashPassword(req.NewPassword)
	if err != nil {
		h.logger.Error("change password: hash", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.logger.Error("change password: begin", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_change_password = 0, updated_at = ? WHERE id = ?`,
		newHash, h.clock.Now(), sess.User.ID,
	); err != nil {
		h.logger.Error("change password: update", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	if err := RevokeAllUserSessions(ctx, tx, h.clock, sess.User.ID, sess.Session.ID); err != nil {
		h.logger.Error("change password: revoke sessions", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	// Family members get an audit entry. Accounts without a family do not.
	if membership, err := membershipOf(ctx, tx, sess.User.ID); err != nil {
		h.logger.Error("change password: membership", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	} else if membership != nil {
		if err := recordPasswordAudit(ctx, tx, membership.FamilyID, sess.User.ID); err != nil {
			h.logger.Error("change password: audit", slog.String("error", err.Error()))
			web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.logger.Error("change password: commit", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	h.limiter.ClearUser(sess.User.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// ThemeRequest is the body of PUT /api/v1/auth/me/theme.
type ThemeRequest struct {
	Theme string `json:"theme"`
}

// SetTheme handles PUT /api/v1/auth/me/theme. It saves the signed-in user's
// theme choice ("system", "light" or "dark") on the server, so it follows the
// user to every device. Any signed-in user may set their own theme.
func (h *Handlers) SetTheme(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}

	ctx := r.Context()
	sess, err := ValidateSession(ctx, h.db, h.clock, cookie.Value)
	if err != nil {
		web.RespondError(w, http.StatusUnauthorized, "session_invalid", "Session is invalid or expired", nil)
		return
	}

	var req ThemeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&req); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return
	}
	switch req.Theme {
	case "system", "light", "dark":
	default:
		web.RespondError(w, http.StatusBadRequest, "bad_request", "theme must be system, light or dark", nil)
		return
	}

	if _, err := h.db.ExecContext(ctx,
		`UPDATE users SET theme = ?, updated_at = ? WHERE id = ?`,
		req.Theme, h.clock.Now(), sess.User.ID,
	); err != nil {
		h.logger.Error("set theme: update", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// recordPasswordAudit writes the password.changed audit entry. It inserts the
// row directly because the audit package imports permissions, which imports
// auth, so auth cannot import audit without a cycle. The columns match
// audit.Record.
func recordPasswordAudit(ctx context.Context, tx *sql.Tx, familyID, userID string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit_logs (id, family_id, actor_id, action, target_type, target_id) VALUES (?, ?, ?, ?, ?, ?)`,
		ids.New(), familyID, userID, "password.changed", "user", userID,
	)
	return err
}

// queryRower is the part of *sql.DB and *sql.Tx used for read queries.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// membershipOf returns the user's active family membership, or nil when there is none.
func membershipOf(ctx context.Context, q queryRower, userID string) (*FamilyMembership, error) {
	var m FamilyMembership
	var avatarID sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT fm.id, fm.family_id, f.name, f.timezone, fm.role_id, r.name, r.is_fixed, fm.status, fm.colour,
		        (SELECT ma.id FROM member_avatars ma WHERE ma.family_member_id = fm.id LIMIT 1)
		 FROM family_members fm
		 JOIN families f ON fm.family_id = f.id
		 JOIN roles r ON fm.role_id = r.id
		 WHERE fm.user_id = ? AND fm.status = 'active'
		 LIMIT 1`,
		userID,
	).Scan(&m.MemberID, &m.FamilyID, &m.FamilyName, &m.Timezone, &m.RoleID, &m.RoleName, &m.IsParent, &m.Status, &m.Colour, &avatarID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if avatarID.Valid {
		m.AvatarID = &avatarID.String
	}
	return &m, nil
}
