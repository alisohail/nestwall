package badges

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

const (
	maxBadgeBody = 4 << 10
	maxIdemKey   = 100 // characters
)

// Handlers serves the badge settings and manual award endpoints (PBI M4-09, batch 09b). Reads need
// settings.view; changes need settings.manage; awards need rewards.manage.
type Handlers struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
}

// NewHandlers creates the badge handlers.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger}
}

// settingsRequest is the body of PUT /api/v1/badges/settings.
type settingsRequest struct {
	Enabled            bool `json:"enabled"`
	ShowOnKidDashboard bool `json:"show_on_kid_dashboard"`
	ShowOnWall         bool `json:"show_on_wall"`
	NotifyChild        bool `json:"notify_child"`
}

// groupRequest is the body of PUT /api/v1/badges/groups/{group}.
type groupRequest struct {
	Enabled bool `json:"enabled"`
}

// customRequest is the body of POST /api/v1/badges/custom and PUT /api/v1/badges/custom/{id}. Enabled
// is ignored on create.
type customRequest struct {
	Name        string  `json:"name"`
	Emoji       string  `json:"emoji"`
	Description *string `json:"description"`
	Enabled     bool    `json:"enabled"`
}

// awardRequest is the body of POST /api/v1/badges/awards.
type awardRequest struct {
	MemberID string `json:"member_id"`
	BadgeID  string `json:"badge_id"`
	Note     string `json:"note"`
}

// GetConfig handles GET /api/v1/badges. The ETag is the settings version.
func (h *Handlers) GetConfig(w http.ResponseWriter, r *http.Request) {
	familyID, _, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	cfg, err := LoadConfig(r.Context(), h.db, familyID)
	if err != nil {
		h.internalError(w, "load badges", err)
		return
	}
	setETag(w, cfg.Version)
	web.RespondJSON(w, http.StatusOK, cfg)
}

// PutSettings handles PUT /api/v1/badges/settings. An If-Match header with the settings version guards
// against a stale write.
func (h *Handlers) PutSettings(w http.ResponseWriter, r *http.Request) {
	familyID, userID, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, ok := expectedVersion(w, r)
	if !ok {
		return
	}
	var req settingsRequest
	if !decodeBadge(w, r, &req) {
		return
	}
	cfg, err := SaveSettings(r.Context(), h.db, h.clock.Now(), familyID, userID, Settings{
		Enabled:            req.Enabled,
		ShowOnKidDashboard: req.ShowOnKidDashboard,
		ShowOnWall:         req.ShowOnWall,
		NotifyChild:        req.NotifyChild,
	}, expected)
	h.respondConfig(w, cfg, err, "save badge settings")
}

// PutGroup handles PUT /api/v1/badges/groups/{group}. It switches every badge in a built-in group on or
// off. It uses the settings version for If-Match.
func (h *Handlers) PutGroup(w http.ResponseWriter, r *http.Request) {
	familyID, userID, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, ok := expectedVersion(w, r)
	if !ok {
		return
	}
	var req groupRequest
	if !decodeBadge(w, r, &req) {
		return
	}
	cfg, err := SetGroup(r.Context(), h.db, h.clock.Now(), familyID, userID, r.PathValue("group"), req.Enabled, expected)
	if errors.Is(err, ErrUnknownGroup) {
		invalid(w, "group", "must be one of the six built-in groups")
		return
	}
	h.respondConfig(w, cfg, err, "save badge group")
}

// CreateCustom handles POST /api/v1/badges/custom. A new badge is on. It returns 201.
func (h *Handlers) CreateCustom(w http.ResponseWriter, r *http.Request) {
	familyID, userID, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	var req customRequest
	if !decodeBadge(w, r, &req) {
		return
	}
	c, err := CreateCustom(r.Context(), h.db, h.clock.Now(), familyID, userID, CustomInput{
		Name:        req.Name,
		Emoji:       req.Emoji,
		Description: req.Description,
	})
	var fe *FieldError
	if errors.As(err, &fe) {
		invalid(w, fe.Field, fe.Reason)
		return
	}
	if err != nil {
		h.internalError(w, "create custom badge", err)
		return
	}
	setETag(w, c.Version)
	web.RespondJSON(w, http.StatusCreated, c)
}

// UpdateCustom handles PUT /api/v1/badges/custom/{id}. The badge's own version is sent as If-Match.
func (h *Handlers) UpdateCustom(w http.ResponseWriter, r *http.Request) {
	familyID, userID, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, ok := expectedVersion(w, r)
	if !ok {
		return
	}
	var req customRequest
	if !decodeBadge(w, r, &req) {
		return
	}
	c, err := UpdateCustom(r.Context(), h.db, h.clock.Now(), familyID, userID, r.PathValue("id"), CustomUpdate{
		CustomInput: CustomInput{Name: req.Name, Emoji: req.Emoji, Description: req.Description},
		Enabled:     req.Enabled,
	}, expected)
	var fe *FieldError
	var stale staleVersionError
	switch {
	case errors.As(err, &fe):
		invalid(w, fe.Field, fe.Reason)
		return
	case errors.Is(err, ErrBadgeNotFound):
		web.RespondError(w, http.StatusNotFound, "badge_not_found", "Badge not found", nil)
		return
	case errors.As(err, &stale):
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"This badge changed since you loaded it. Reload and try again.",
			map[string]any{"version": stale.Current})
		return
	case err != nil:
		h.internalError(w, "update custom badge", err)
		return
	}
	setETag(w, c.Version)
	web.RespondJSON(w, http.StatusOK, c)
}

// PostAward handles POST /api/v1/badges/awards. It gives a custom badge to a child by hand. It needs an
// Idempotency-Key header. A new award returns 201; a replay returns 200 with the stored award.
func (h *Handlers) PostAward(w http.ResponseWriter, r *http.Request) {
	familyID, userID, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || utf8.RuneCountInString(key) > maxIdemKey {
		invalid(w, "Idempotency-Key", "required, up to 100 characters")
		return
	}
	var req awardRequest
	if !decodeBadge(w, r, &req) {
		return
	}
	res, err := Award(r.Context(), h.db, h.clock.Now(), familyID, userID, memberID, AwardInput{
		MemberID:       req.MemberID,
		BadgeID:        req.BadgeID,
		Note:           req.Note,
		IdempotencyKey: key,
	})
	var fe *FieldError
	switch {
	case errors.As(err, &fe):
		invalid(w, fe.Field, fe.Reason)
		return
	case errors.Is(err, ErrMemberNotFound):
		web.RespondError(w, http.StatusNotFound, "member_not_found", "Member not found", nil)
		return
	case errors.Is(err, ErrBadgeNotFound):
		web.RespondError(w, http.StatusNotFound, "badge_not_found", "Badge not found", nil)
		return
	case errors.Is(err, ErrNotCustom):
		invalid(w, "badge_id", "built-in badges are earned automatically")
		return
	case errors.Is(err, ErrBadgeDisabled):
		web.RespondError(w, http.StatusConflict, "badge_disabled", "This badge is off", nil)
		return
	case errors.Is(err, ErrBadgesOff):
		web.RespondError(w, http.StatusConflict, "badges_off", "Badges are off for the family", nil)
		return
	case errors.Is(err, ErrIdempotencyConflict):
		web.RespondError(w, http.StatusConflict, "idempotency_conflict",
			"The Idempotency-Key was used for a different award", nil)
		return
	case err != nil:
		h.internalError(w, "award badge", err)
		return
	}

	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	web.RespondJSON(w, status, res)
}

// respondConfig writes the full config with its version as the ETag, or a 412 when the version is stale.
func (h *Handlers) respondConfig(w http.ResponseWriter, cfg Config, err error, what string) {
	var stale staleVersionError
	if errors.As(err, &stale) {
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"These badge settings changed since you loaded them. Reload and try again.",
			map[string]any{"version": stale.Current})
		return
	}
	if err != nil {
		h.internalError(w, what, err)
		return
	}
	setETag(w, cfg.Version)
	web.RespondJSON(w, http.StatusOK, cfg)
}

// actor returns the signed-in member's family and IDs. It writes the error response and returns
// ok=false when the request may not continue.
func (h *Handlers) actor(w http.ResponseWriter, r *http.Request) (familyID, userID, memberID string, ok bool) {
	ctx := r.Context()
	memberID, okMember := permissions.GetFamilyMemberID(ctx)
	userID, okUser := permissions.GetUserID(ctx)
	if !okMember || !okUser {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return "", "", "", false
	}
	err := h.db.QueryRowContext(ctx,
		`SELECT family_id FROM family_members WHERE id = ? AND status = 'active'`, memberID,
	).Scan(&familyID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No active family membership", nil)
		return "", "", "", false
	}
	if err != nil {
		h.internalError(w, "load membership", err)
		return "", "", "", false
	}
	return familyID, userID, memberID, true
}

// expectedVersion reads the If-Match version. It returns -1 when the header is absent. It writes a 400
// response and returns ok=false for a malformed header.
func expectedVersion(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return -1, true
	}
	n, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(raw, "W/"), `"`))
	if err != nil || n < 0 {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid If-Match header", nil)
		return 0, false
	}
	return n, true
}

// setETag sets the ETag header to the quoted version.
func setETag(w http.ResponseWriter, version int) {
	w.Header().Set("ETag", `"`+strconv.Itoa(version)+`"`)
}

// decodeBadge decodes a bounded JSON body and rejects unknown fields. It writes a 400 response and
// returns false on failure.
func decodeBadge(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBadgeBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return false
	}
	return true
}

// invalid writes a 400 validation_failed response with the field and reason.
func invalid(w http.ResponseWriter, field, reason string) {
	web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input",
		map[string]any{"field": field, "reason": reason})
}

// internalError logs the cause and writes a generic 500 response.
func (h *Handlers) internalError(w http.ResponseWriter, what string, err error) {
	h.logger.Error("badges: "+what, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}
