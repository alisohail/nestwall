package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"nestwall/backend/internal/badges/catalogue"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
	"nestwall/backend/internal/tasks/seed"
)

// ParentRoleID is the fixed UUID of the Parent role seeded by migration 000002.
const ParentRoleID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

// parentColour is the default colour for the first parent.
const parentColour = "#6366F1"

// SetupStatusResponse represents the setup status response
type SetupStatusResponse struct {
	SetupRequired bool   `json:"setup_required"`
	Message       string `json:"message,omitempty"`
}

// SetupParentRequest represents the request to create first parent
type SetupParentRequest struct {
	UserID      string `json:"user_id"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	FamilyName  string `json:"family_name"`
	Timezone    string `json:"timezone"`
	Locale      string `json:"locale"`
}

// SetupParentResponse represents the response after successful setup
type SetupParentResponse struct {
	User     UserResponse `json:"user"`
	FamilyId string       `json:"family_id"`
	Message  string       `json:"message"`
}

// SetupHandlers contains handlers for setup endpoints
type SetupHandlers struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
}

// NewSetupHandlers creates setup handlers
func NewSetupHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger) *SetupHandlers {
	return &SetupHandlers{
		db:     db,
		clock:  clk,
		logger: logger,
	}
}

// Status handles GET /api/v1/setup/status
func (h *SetupHandlers) Status(w http.ResponseWriter, r *http.Request) {
	var count int
	if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM families`).Scan(&count); err != nil {
		h.logger.Error("setup status: count families", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	setupRequired := count == 0
	resp := SetupStatusResponse{SetupRequired: setupRequired}
	if !setupRequired {
		resp.Message = "Setup already complete"
	}
	web.RespondJSON(w, http.StatusOK, resp)
}

// Parent handles POST /api/v1/setup/parent. It creates the family and the first
// parent account in one transaction. It does not sign the parent in.
func (h *SetupHandlers) Parent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var count int
	if err := h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM families`).Scan(&count); err != nil {
		h.logger.Error("setup: count families", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	if count > 0 {
		web.RespondError(w, http.StatusConflict, "setup_complete", "Setup is already complete", nil)
		return
	}

	var req SetupParentRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&req); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return
	}

	if err := ValidateNewUser(req.UserID, req.Password, req.DisplayName); err != nil {
		respondValidation(w, err)
		return
	}
	// The first parent is always a family admin, and admins keep an email for password recovery.
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" {
		respondValidation(w, &ValidationError{Field: "email", Reason: "required"})
		return
	}
	if err := ValidateEmail(req.Email); err != nil {
		respondValidation(w, err)
		return
	}
	if err := ValidateName("family_name", req.FamilyName); err != nil {
		respondValidation(w, err)
		return
	}

	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if err := ValidateTimezone(req.Timezone); err != nil {
		respondValidation(w, err)
		return
	}
	if req.Locale == "" {
		req.Locale = "en"
	}
	if err := ValidateLocale(req.Locale); err != nil {
		respondValidation(w, err)
		return
	}

	passwordHash, err := HashPassword(req.Password)
	if err != nil {
		h.logger.Error("setup: hash password", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.logger.Error("setup: begin transaction", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	defer tx.Rollback()

	userID := ids.New()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, email, is_child)
		 VALUES (?, ?, ?, ?, ?, 0)`,
		userID, req.UserID, passwordHash, strings.TrimSpace(req.DisplayName), req.Email,
	)
	if err != nil {
		if IsUniqueViolation(err) {
			if strings.Contains(err.Error(), "users_email_unique") {
				web.RespondError(w, http.StatusConflict, "email_taken", "Email is already used by another member", nil)
				return
			}
			web.RespondError(w, http.StatusConflict, "user_id_taken", "User ID is already taken", nil)
			return
		}
		h.logger.Error("setup: create user", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	settings, err := json.Marshal(map[string]string{"locale": req.Locale})
	if err != nil {
		h.logger.Error("setup: encode settings", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	familyID := ids.New()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO families (id, name, timezone, settings)
		 VALUES (?, ?, ?, ?)`,
		familyID, strings.TrimSpace(req.FamilyName), req.Timezone, string(settings),
	)
	if err != nil {
		// The families.singleton_lock UNIQUE constraint rejects a second family
		// created by a concurrent setup request.
		if IsUniqueViolation(err) {
			web.RespondError(w, http.StatusConflict, "setup_complete", "Setup is already complete", nil)
			return
		}
		h.logger.Error("setup: create family", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	if err := seed.SeedDefaultCategories(ctx, tx, familyID); err != nil {
		h.logger.Error("setup: seed task categories", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	if err := catalogue.SeedDefaults(ctx, tx, familyID); err != nil {
		h.logger.Error("setup: seed badges", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'active', ?, ?, ?)`,
		ids.New(), familyID, userID, ParentRoleID, parentColour, h.clock.Now(), h.clock.Now(),
	)
	if err != nil {
		h.logger.Error("setup: create membership", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	if err := tx.Commit(); err != nil {
		h.logger.Error("setup: commit", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	web.RespondJSON(w, http.StatusCreated, SetupParentResponse{
		User: UserResponse{
			ID:          userID,
			UserID:      req.UserID,
			DisplayName: strings.TrimSpace(req.DisplayName),
			IsChild:     false,
		},
		FamilyId: familyID,
		Message:  "Setup complete. Sign in to continue.",
	})
}

// respondValidation writes a 400 response for a ValidationError, or a generic 400 otherwise.
func respondValidation(w http.ResponseWriter, err error) {
	var vErr *ValidationError
	if errors.As(err, &vErr) {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input", vErr.Params())
		return
	}
	web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input", nil)
}
