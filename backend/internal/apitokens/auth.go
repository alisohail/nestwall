package apitokens

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

// Require wraps a handler so that it accepts only a live API token that carries scope. The token's owner
// must still be an active family member who holds the permission behind the scope, so a token never
// outlives the owner's access. It panics when scope is unknown, because that is a wiring mistake.
func Require(db *sql.DB, clk clock.Clock, scope string) func(next http.HandlerFunc) http.HandlerFunc {
	perm, ok := permissionForScope(scope)
	if !ok {
		panic("apitokens: unknown scope " + scope)
	}
	policy := permissions.NewPolicy(db)

	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			raw, ok := bearerToken(r)
			if !ok {
				web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "API token required", nil)
				return
			}

			ctx := r.Context()
			var tokenID, userID, memberID, scopesJSON string
			var expiresAt time.Time
			var revokedAt sql.NullTime
			err := db.QueryRowContext(ctx,
				`SELECT t.id, t.created_by, fm.id, t.scopes, t.expires_at, t.revoked_at
				 FROM api_tokens t
				 JOIN family_members fm ON fm.user_id = t.created_by AND fm.family_id = t.family_id AND fm.status = 'active'
				 WHERE t.token_hash = ?`,
				auth.HashToken(raw),
			).Scan(&tokenID, &userID, &memberID, &scopesJSON, &expiresAt, &revokedAt)
			if errors.Is(err, sql.ErrNoRows) {
				web.RespondError(w, http.StatusUnauthorized, "token_invalid", "API token is invalid", nil)
				return
			}
			if err != nil {
				web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
				return
			}
			now := clk.Now().UTC()
			if revokedAt.Valid || !now.Before(expiresAt) {
				web.RespondError(w, http.StatusUnauthorized, "token_invalid", "API token is invalid", nil)
				return
			}

			var granted []string
			if err := json.Unmarshal([]byte(scopesJSON), &granted); err != nil {
				web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
				return
			}
			if !slices.Contains(granted, scope) {
				web.RespondError(w, http.StatusForbidden, "insufficient_scope", "API token lacks the "+scope+" scope", nil)
				return
			}

			res, err := policy.Check(ctx, memberID, perm)
			if err != nil {
				web.RespondError(w, http.StatusInternalServerError, "internal_error", "Permission check failed", nil)
				return
			}
			if !res.Allowed {
				web.RespondError(w, http.StatusForbidden, "insufficient_scope", "The token owner no longer has access to "+scope, nil)
				return
			}

			if _, err := db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, now, tokenID); err != nil {
				web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
				return
			}

			ctx = context.WithValue(ctx, permissions.ContextKeyFamilyMemberID, memberID)
			ctx = context.WithValue(ctx, permissions.ContextKeyUserID, userID)
			next(w, r.WithContext(ctx))
		}
	}
}

// bearerToken returns the raw token from an "Authorization: Bearer nst_..." header.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || !strings.HasPrefix(token, tokenPrefix) {
		return "", false
	}
	return token, true
}
