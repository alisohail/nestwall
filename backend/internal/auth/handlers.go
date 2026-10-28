package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"nestwall/backend/internal/buildinfo"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

// SessionCookieName is the name of the session cookie.
const SessionCookieName = "nestwall-session"

// maxJSONBody limits request bodies for auth endpoints.
const maxJSONBody = 64 << 10

// dummyHash is verified when the user ID is unknown, so that a failed login
// takes as long as a real password check and does not reveal which accounts exist.
var dummyHash = mustHashPassword("nestwall-dummy-password")

func mustHashPassword(password string) string {
	hash, err := HashPassword(password)
	if err != nil {
		panic("auth: cannot create dummy hash: " + err.Error())
	}
	return hash
}

// LoginRequest represents login request body
type LoginRequest struct {
	UserID   string `json:"user_id"`
	Password string `json:"password"`
}

// LoginResponse represents login response body
type LoginResponse struct {
	User UserResponse `json:"user"`
}

// MeResponse represents /me response body
type MeResponse struct {
	User    UserResponse      `json:"user"`
	Family  *FamilyMembership `json:"family"`
	Version string            `json:"version"`
}

// FamilyMembership represents a user's active membership in a family
type FamilyMembership struct {
	MemberID   string  `json:"member_id"`
	FamilyID   string  `json:"family_id"`
	FamilyName string  `json:"family_name"`
	Timezone   string  `json:"timezone"`
	TimeFormat string  `json:"time_format"`
	RoleID     string  `json:"role_id"`
	RoleName   string  `json:"role_name"`
	IsParent   bool    `json:"is_parent"`
	Status     string  `json:"status"`
	Colour     string  `json:"colour"`
	AvatarID   *string `json:"avatar_id"`
}

// UserResponse represents user info in responses
type UserResponse struct {
	ID                 string  `json:"id"`
	UserID             string  `json:"user_id"`
	Email              *string `json:"email,omitempty"`
	DisplayName        string  `json:"display_name"`
	IsChild            bool    `json:"is_child"`
	MustChangePassword bool    `json:"must_change_password"`
	DateOfBirth        *string `json:"date_of_birth,omitempty"`
	Gender             *string `json:"gender,omitempty"`
	Theme              string  `json:"theme"`
}

// Handlers type for auth endpoints
type Handlers struct {
	db      *sql.DB
	limiter *RateLimiter
	clock   clock.Clock
	logger  *slog.Logger
	// mailer and appURL are set by WithEmailReset. Until then no reset email is sent.
	mailer ResetMailer
	appURL string
}

// NewHandlers creates auth handlers
func NewHandlers(db *sql.DB, limiter *RateLimiter, clk clock.Clock, logger *slog.Logger) *Handlers {
	return &Handlers{
		db:      db,
		limiter: limiter,
		clock:   clk,
		logger:  logger,
	}
}

// Login handles POST /api/v1/auth/login
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)

	var req LoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&req); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return
	}
	if req.UserID == "" || req.Password == "" {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "user_id and password are required", nil)
		return
	}

	if err := h.limiter.CheckRateLimit(ip, req.UserID); err != nil {
		web.RespondError(w, http.StatusTooManyRequests, "rate_limited", "Too many login attempts. Try again later.", nil)
		return
	}

	ctx := r.Context()
	var userUUID, userIDDB, displayName, passwordHash, theme string
	var email *string
	var isChild, mustChange bool

	err := h.db.QueryRowContext(ctx,
		`SELECT id, user_id, email, display_name, is_child, must_change_password, password_hash, theme
		 FROM users WHERE user_id = ?`,
		req.UserID,
	).Scan(&userUUID, &userIDDB, &email, &displayName, &isChild, &mustChange, &passwordHash, &theme)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.logger.Error("login: query user", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	found := err == nil
	hashToCheck := dummyHash
	if found {
		hashToCheck = passwordHash
	}
	passwordOK := VerifyPassword(req.Password, hashToCheck)
	if !found || !passwordOK {
		h.limiter.RecordFailure(ip, req.UserID)
		web.RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid user ID or password", nil)
		return
	}

	session, err := CreateSession(ctx, h.db, h.clock, userUUID, r.UserAgent())
	if err != nil {
		h.logger.Error("login: create session", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	h.limiter.ClearUser(req.UserID)
	setSessionCookie(w, r, session.Token)

	web.RespondJSON(w, http.StatusOK, LoginResponse{
		User: UserResponse{
			ID:                 userUUID,
			UserID:             userIDDB,
			Email:              email,
			DisplayName:        displayName,
			IsChild:            isChild,
			MustChangePassword: mustChange,
			Theme:              theme,
		},
	})
}

// Logout handles POST /api/v1/auth/logout
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		if err := RevokeSession(r.Context(), h.db, h.clock, cookie.Value); err != nil {
			h.logger.Error("logout: revoke session", slog.String("error", err.Error()))
			web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
			return
		}
	}

	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// Me handles GET /api/v1/auth/me
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
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

	family, err := h.membership(ctx, sess.User.ID)
	if err != nil {
		h.logger.Error("me: membership", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	web.RespondJSON(w, http.StatusOK, MeResponse{
		User: UserResponse{
			ID:                 sess.User.ID,
			UserID:             sess.User.UserID,
			Email:              sess.User.Email,
			DisplayName:        sess.User.DisplayName,
			IsChild:            sess.User.IsChild,
			MustChangePassword: sess.User.MustChangePassword,
			DateOfBirth:        sess.User.DateOfBirth,
			Gender:             sess.User.Gender,
			Theme:              sess.User.Theme,
		},
		Family:  family,
		Version: buildinfo.Version,
	})
}

// membership returns the user's active membership in the installation's
// family, or nil when the user has none.
func (h *Handlers) membership(ctx context.Context, userID string) (*FamilyMembership, error) {
	return membershipOf(ctx, h.db, userID)
}

// ClientIP returns the address used for rate limiting. NestWall always runs
// behind its own reverse proxy, which appends the address it received to
// X-Forwarded-For. The right-most entry is therefore the one the proxy saw,
// and a client cannot forge it by sending its own header.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
