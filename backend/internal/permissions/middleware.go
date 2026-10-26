package permissions

import (
	"context"
	"database/sql"
	"net/http"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

const (
	// ContextKeyFamilyMemberID is the key for storing family member ID in context.
	ContextKeyFamilyMemberID contextKey = "family_member_id"
	// ContextKeyUserID is the key for storing the signed-in user ID in context.
	ContextKeyUserID contextKey = "user_id"
	// ContextKeySessionID is the key for storing the current session ID in context.
	ContextKeySessionID contextKey = "session_id"
	// ContextKeyPermission is the key for storing the required permission in context.
	ContextKeyPermission contextKey = "required_permission"
)

// Middleware returns a wrapper that authenticates the session cookie, finds the
// caller's active family membership and checks the permission code. On success
// the family member, user and session IDs are stored in the request context.
//
// A user whose password is temporary (must_change_password) gets 403
// password_change_required on every wrapped route. The change-password route is
// not wrapped, so the user can still replace the password.
func Middleware(db *sql.DB, clk clock.Clock) func(permissionCode string, next http.HandlerFunc) http.HandlerFunc {
	policy := NewPolicy(db)

	return func(permissionCode string, next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx, familyMemberID, ok := authenticate(w, r, db, clk)
			if !ok {
				return
			}

			result, err := policy.Check(ctx, familyMemberID, permissionCode)
			if err != nil {
				web.RespondError(w, http.StatusInternalServerError, "internal_error", "Permission check failed", nil)
				return
			}
			if !result.Allowed {
				web.RespondError(w, http.StatusForbidden, "forbidden", "You do not have permission for this action", nil)
				return
			}

			next(w, r.WithContext(ctx))
		}
	}
}

// RequireParent works like Middleware but admits only the fixed Parent role. Use it for
// actions that no permission code can express, such as pairing a wall display.
func RequireParent(db *sql.DB, clk clock.Clock) func(next http.HandlerFunc) http.HandlerFunc {
	policy := NewPolicy(db)

	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx, familyMemberID, ok := authenticate(w, r, db, clk)
			if !ok {
				return
			}

			isParent, err := policy.IsParent(ctx, familyMemberID)
			if err != nil {
				web.RespondError(w, http.StatusInternalServerError, "internal_error", "Permission check failed", nil)
				return
			}
			if !isParent {
				web.RespondError(w, http.StatusForbidden, "forbidden", "Only a parent can do this", nil)
				return
			}

			next(w, r.WithContext(ctx))
		}
	}
}

// Authenticated works like Middleware but skips the permission check. It still requires a valid
// session and an active family membership. Use it only when the handler scopes the data itself,
// for example a child reading their own picture.
func Authenticated(db *sql.DB, clk clock.Clock) func(next http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx, _, ok := authenticate(w, r, db, clk)
			if !ok {
				return
			}
			next(w, r.WithContext(ctx))
		}
	}
}

// authenticate checks the session cookie and the active membership. It writes the error response and
// returns ok=false when the request may not continue. On success it returns the context with the
// family member, user and session IDs set.
func authenticate(w http.ResponseWriter, r *http.Request, db *sql.DB, clk clock.Clock) (context.Context, string, bool) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return nil, "", false
	}

	sess, err := auth.ValidateSession(r.Context(), db, clk, cookie.Value)
	if err != nil {
		web.RespondError(w, http.StatusUnauthorized, "session_invalid", "Session is invalid or expired", nil)
		return nil, "", false
	}

	if sess.User.MustChangePassword {
		web.RespondError(w, http.StatusForbidden, "password_change_required", "You must choose a new password first", nil)
		return nil, "", false
	}

	// NestWall supports one family per installation, so the user has at most one active membership.
	var familyMemberID string
	err = db.QueryRowContext(r.Context(),
		`SELECT id FROM family_members WHERE user_id = ? AND status = 'active' LIMIT 1`,
		sess.User.ID,
	).Scan(&familyMemberID)
	if err != nil {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No active family membership", nil)
		return nil, "", false
	}

	ctx := context.WithValue(r.Context(), ContextKeyFamilyMemberID, familyMemberID)
	ctx = context.WithValue(ctx, ContextKeyUserID, sess.User.ID)
	ctx = context.WithValue(ctx, ContextKeySessionID, sess.Session.ID)
	return ctx, familyMemberID, true
}

// GetFamilyMemberID retrieves the family member ID from the request context.
func GetFamilyMemberID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ContextKeyFamilyMemberID).(string)
	return id, ok
}

// GetUserID retrieves the signed-in user ID from the request context.
func GetUserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ContextKeyUserID).(string)
	return id, ok
}

// GetSessionID retrieves the current session ID from the request context.
func GetSessionID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ContextKeySessionID).(string)
	return id, ok
}
