// Package sessions lists and ends sign-in sessions. Members manage their own
// sessions. Parents can see and end the sessions of other members in their family.
package sessions

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

// Handlers serves the session endpoints.
type Handlers struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger}
}

// SessionView is one active session as shown in the UI. The token is never returned.
type SessionView struct {
	ID        string    `json:"id"`
	UserAgent string    `json:"user_agent"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Current   bool      `json:"current"`
}

// ListSessionsResponse wraps the session list.
type ListSessionsResponse struct {
	Sessions []SessionView `json:"sessions"`
}

// ListOwn handles GET /api/v1/auth/sessions. It lists the caller's active sessions.
func (h *Handlers) ListOwn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, ok := permissions.GetUserID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}
	currentID, _ := permissions.GetSessionID(ctx)

	sessions, err := h.listActive(r, userID, currentID)
	if err != nil {
		h.internalError(w, "failed to list sessions", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, ListSessionsResponse{Sessions: sessions})
}

// RevokeOwn handles DELETE /api/v1/auth/sessions/{id}. A user can end only their own sessions.
func (h *Handlers) RevokeOwn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, ok := permissions.GetUserID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid session ID", nil)
		return
	}

	res, err := h.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		h.clock.Now(), sessionID, userID,
	)
	if err != nil {
		h.internalError(w, "failed to revoke session", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		web.RespondError(w, http.StatusNotFound, "not_found", "Session not found", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RevokeOthers handles POST /api/v1/auth/sessions/revoke-others. It ends every
// session of the caller except the current one.
func (h *Handlers) RevokeOthers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, ok := permissions.GetUserID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}
	currentID, _ := permissions.GetSessionID(ctx)

	if _, err := h.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND id <> ? AND revoked_at IS NULL`,
		h.clock.Now(), userID, currentID,
	); err != nil {
		h.internalError(w, "failed to revoke sessions", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListForMember handles GET /api/v1/family/members/{id}/sessions (sessions.view).
// The member must be in the caller's family; otherwise the response is 404.
func (h *Handlers) ListForMember(w http.ResponseWriter, r *http.Request) {
	userID, found, ok := h.memberUser(w, r)
	if !ok {
		return
	}
	if !found {
		web.RespondError(w, http.StatusNotFound, "not_found", "Member not found", nil)
		return
	}

	sessions, err := h.listActive(r, userID, "")
	if err != nil {
		h.internalError(w, "failed to list sessions", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, ListSessionsResponse{Sessions: sessions})
}

// RevokeAllForMember handles POST /api/v1/family/members/{id}/sessions/revoke-all
// (members.manage). It ends every session of the member. The member's sign-in
// session is ended too, so the member must sign in again.
func (h *Handlers) RevokeAllForMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, found, ok := h.memberUser(w, r)
	if !ok {
		return
	}
	if !found {
		web.RespondError(w, http.StatusNotFound, "not_found", "Member not found", nil)
		return
	}

	if _, err := h.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		h.clock.Now(), userID,
	); err != nil {
		h.internalError(w, "failed to revoke sessions", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// memberUser resolves the {id} member in the caller's family. It returns the
// member's user ID and whether the member was found. ok is false when the
// response has already been written.
func (h *Handlers) memberUser(w http.ResponseWriter, r *http.Request) (string, bool, bool) {
	ctx := r.Context()
	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return "", false, false
	}
	targetID := r.PathValue("id")
	if targetID == "" {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid member ID", nil)
		return "", false, false
	}

	var userID, familyID string
	err := h.db.QueryRowContext(ctx,
		`SELECT target.user_id, target.family_id
		 FROM family_members target
		 JOIN family_members actor ON actor.family_id = target.family_id AND actor.id = ?
		 WHERE target.id = ?`,
		actorMemberID, targetID,
	).Scan(&userID, &familyID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, true
	}
	if err != nil {
		h.internalError(w, "failed to find member", err)
		return "", false, false
	}
	return userID, true, true
}

// listActive returns the active (not revoked, not expired) sessions of a user.
func (h *Handlers) listActive(r *http.Request, userID, currentID string) ([]SessionView, error) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, COALESCE(user_agent, ''), created_at, expiry
		 FROM sessions
		 WHERE user_id = ? AND revoked_at IS NULL AND expiry > ?
		 ORDER BY created_at DESC`,
		userID, h.clock.Now(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SessionView{}
	for rows.Next() {
		var s SessionView
		if err := rows.Scan(&s.ID, &s.UserAgent, &s.CreatedAt, &s.ExpiresAt); err != nil {
			return nil, err
		}
		s.Current = s.ID == currentID
		out = append(out, s)
	}
	return out, rows.Err()
}

// internalError logs the error and writes a generic 500 response.
func (h *Handlers) internalError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
}
