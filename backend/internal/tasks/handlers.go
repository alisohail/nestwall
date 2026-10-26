// Package tasks implements task categories, part-of-day times and the family-wide task
// defaults (PBI M3-02). Writes are versioned with If-Match and audited, as in the family
// permissions handlers.
package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

// maxJSONBody bounds every request body in this package.
const maxJSONBody = 64 << 10

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Handlers serves the task endpoints. The clock sets the time that actions are checked against.
type Handlers struct {
	db           *sql.DB
	clock        clock.Clock
	logger       *slog.Logger
	listeners    []func(ctx context.Context, e Event)
	participants []Participant
}

// NewHandlers creates the task handlers.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger}
}

// querier is satisfied by both *sql.DB and *sql.Tx, so read helpers work in either.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// memberFamily returns the family of the signed-in member and the user ID. It writes the
// error response and returns ok=false when the request may not continue.
func (h *Handlers) memberFamily(w http.ResponseWriter, r *http.Request) (familyID, userID string, ok bool) {
	ctx := r.Context()
	memberID, okMember := permissions.GetFamilyMemberID(ctx)
	userID, okUser := permissions.GetUserID(ctx)
	if !okMember || !okUser {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return "", "", false
	}

	err := h.db.QueryRowContext(ctx,
		`SELECT family_id FROM family_members WHERE id = ? AND status = 'active'`, memberID,
	).Scan(&familyID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No active family membership", nil)
		return "", "", false
	}
	if err != nil {
		h.internalError(w, "load membership", err)
		return "", "", false
	}
	return familyID, userID, true
}

// decodeBody decodes a bounded JSON body and rejects unknown fields. It writes a 400
// response and returns false on failure.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return false
	}
	return true
}

// decodeOptionalBody is decodeBody for an action whose body may be empty. An empty body leaves v
// unset and is not an error.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return false
	}
	return true
}

// respondValidation writes a 400 validation_failed response with the field and reason.
func respondValidation(w http.ResponseWriter, err error) {
	var fieldErr *FieldError
	if errors.As(err, &fieldErr) {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input",
			map[string]any{"field": fieldErr.Field, "reason": fieldErr.Reason})
		return
	}
	web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input", nil)
}

// internalError logs the cause and writes a generic 500 response.
func (h *Handlers) internalError(w http.ResponseWriter, what string, err error) {
	h.logger.Error("tasks: "+what, "error", err)
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}

// isUUID reports whether s is a canonical UUID string.
func isUUID(s string) bool {
	return uuidPattern.MatchString(s)
}
