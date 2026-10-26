package apitokens

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

const (
	// tokenPrefix marks an API token so it is easy to spot in a config file and to tell apart from a session.
	tokenPrefix   = "nst_"
	maxBodyBytes  = 16 << 10
	maxNameLength = 60
)

// expiryChoices are the lifetimes the settings screen offers, in days.
var expiryChoices = []int{30, 90, 365}

// Creation limit per member. Each token is a long-lived credential, so a burst of creates is suspicious.
const (
	createMaxAttempts = 10
	createWindow      = time.Hour
)

// Handlers serves the family's API token settings.
type Handlers struct {
	db      *sql.DB
	clk     clock.Clock
	logger  *slog.Logger
	limiter *auth.RateLimiter
}

// NewHandlers returns the API token handlers.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, clk: clk, logger: logger, limiter: auth.NewRateLimiter(clk)}
}

// tokenJSON is one token as the settings list shows it. The raw token is never part of it.
type tokenJSON struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	TokenHint  string     `json:"token_hint"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// createRequest is the body of POST /api/v1/settings/api-tokens.
type createRequest struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays int      `json:"expires_in_days"`
}

// createResponse adds the raw token, which is shown once.
type createResponse struct {
	tokenJSON
	Token string `json:"token"`
}

// List returns the family's active tokens, newest first.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}
	familyID, _, err := actorFamily(ctx, h.db, memberID)
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}

	rows, err := h.db.QueryContext(ctx,
		`SELECT id, name, scopes, token_prefix, token_suffix, expires_at, last_used_at, created_at
		 FROM api_tokens WHERE family_id = ? AND revoked_at IS NULL ORDER BY created_at DESC`,
		familyID,
	)
	if err != nil {
		h.internalError(w, "failed to list api tokens", err)
		return
	}
	defer rows.Close()

	out := []tokenJSON{}
	for rows.Next() {
		var t tokenJSON
		var scopesJSON, prefix, suffix string
		var lastUsed sql.NullTime
		if err := rows.Scan(&t.ID, &t.Name, &scopesJSON, &prefix, &suffix, &t.ExpiresAt, &lastUsed, &t.CreatedAt); err != nil {
			h.internalError(w, "failed to read api token", err)
			return
		}
		if err := json.Unmarshal([]byte(scopesJSON), &t.Scopes); err != nil {
			h.internalError(w, "failed to decode api token scopes", err)
			return
		}
		t.TokenHint = prefix + "…" + suffix
		if lastUsed.Valid {
			ts := lastUsed.Time
			t.LastUsedAt = &ts
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "failed to list api tokens", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, map[string]any{"items": out})
}

// Create makes a token for the calling user. It refuses any scope the user does not hold, and returns
// the raw token once. Only its hash is stored.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}

	limitKey := "api-token-create:" + memberID
	if err := h.limiter.CheckWindow(limitKey, createMaxAttempts, createWindow); err != nil {
		web.RespondError(w, http.StatusTooManyRequests, "rate_limited", "Too many tokens created. Try again later.", nil)
		return
	}
	h.limiter.RecordWindow(limitKey, createWindow)

	var body createRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameLength {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Name must be between 1 and 60 characters", nil)
		return
	}
	scopes, msg := normalizeScopes(body.Scopes)
	if msg != "" {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", msg, nil)
		return
	}
	if !slices.Contains(expiryChoices, body.ExpiresInDays) {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Choose an expiry of 30, 90 or 365 days", nil)
		return
	}

	familyID, userID, err := actorFamily(ctx, h.db, memberID)
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}

	policy := permissions.NewPolicy(h.db)
	for _, scope := range scopes {
		perm, _ := permissionForScope(scope)
		res, err := policy.Check(ctx, memberID, perm)
		if err != nil {
			h.internalError(w, "failed to check scope permission", err)
			return
		}
		if !res.Allowed {
			web.RespondError(w, http.StatusForbidden, "scope_not_allowed",
				fmt.Sprintf("You cannot grant the %s scope", scope), map[string]any{"scope": scope})
			return
		}
	}

	secret, err := auth.GenerateSessionToken()
	if err != nil {
		h.internalError(w, "failed to generate api token", err)
		return
	}
	raw := tokenPrefix + secret
	now := h.clk.Now().UTC()
	expiresAt := now.Add(time.Duration(body.ExpiresInDays) * 24 * time.Hour)
	scopesJSON, err := json.Marshal(scopes)
	if err != nil {
		h.internalError(w, "failed to encode api token scopes", err)
		return
	}

	tokenID := ids.New()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO api_tokens (id, family_id, name, token_hash, token_prefix, token_suffix, scopes, expires_at, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tokenID, familyID, name, auth.HashToken(raw), raw[:8], raw[len(raw)-2:], string(scopesJSON),
		expiresAt, userID, now, now,
	); err != nil {
		h.internalError(w, "failed to insert api token", err)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "api_token.created", "api_token", &tokenID,
		map[string]any{"name": name, "scopes": scopes}); err != nil {
		h.internalError(w, "failed to record audit event", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	web.RespondJSON(w, http.StatusCreated, createResponse{
		tokenJSON: tokenJSON{
			ID:        tokenID,
			Name:      name,
			Scopes:    scopes,
			TokenHint: raw[:8] + "…" + raw[len(raw)-2:],
			ExpiresAt: expiresAt,
			CreatedAt: now,
		},
		Token: raw,
	})
}

// Revoke stops a token. Its next request is refused. The row stays so the audit trail still names it.
func (h *Handlers) Revoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}
	familyID, userID, err := actorFamily(ctx, h.db, memberID)
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}
	tokenID := r.PathValue("id")
	now := h.clk.Now().UTC()

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	var name string
	err = tx.QueryRowContext(ctx,
		`SELECT name FROM api_tokens WHERE family_id = ? AND id = ? AND revoked_at IS NULL`,
		familyID, tokenID,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusNotFound, "not_found", "API token not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find api token", err)
		return
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ?, updated_at = ? WHERE family_id = ? AND id = ?`,
		now, now, familyID, tokenID,
	); err != nil {
		h.internalError(w, "failed to revoke api token", err)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "api_token.revoked", "api_token", &tokenID,
		map[string]any{"name": name}); err != nil {
		h.internalError(w, "failed to record audit event", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// normalizeScopes trims, de-duplicates and sorts the requested scopes. It returns a user-facing message
// instead of scopes when the list is empty or contains an unknown scope.
func normalizeScopes(in []string) ([]string, string) {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if _, ok := permissionForScope(s); !ok {
			return nil, fmt.Sprintf("Unknown scope %s", s)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, "Choose at least one scope"
	}
	slices.Sort(out)
	return out, ""
}

// actorFamily returns the family ID and user ID of the calling family member.
func actorFamily(ctx context.Context, db *sql.DB, memberID string) (string, string, error) {
	var familyID, userID string
	err := db.QueryRowContext(ctx,
		`SELECT family_id, user_id FROM family_members WHERE id = ?`,
		memberID,
	).Scan(&familyID, &userID)
	return familyID, userID, err
}

// decodeJSON decodes a bounded JSON body that must not contain unknown fields. It writes a 400 and returns false on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return false
	}
	return true
}

func (h *Handlers) internalError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, "error", err)
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}
