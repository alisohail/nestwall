package rewards

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

const (
	maxPoints     = 100000 // largest magnitude one manual change may carry
	maxReason     = 200    // characters
	maxIdemKey    = 100    // characters
	maxRewardBody = 4 << 10
)

// Handlers serves the parent points endpoints. Every route needs rewards.manage.
type Handlers struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
}

// NewHandlers creates the rewards handlers.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger}
}

// pointsRequest is the body of POST /api/v1/family/members/{id}/points.
type pointsRequest struct {
	Type   string `json:"type"`
	Points int    `json:"points"`
	Reason string `json:"reason"`
}

// reverseRequest is the body of POST /api/v1/points/entries/{id}/reverse.
type reverseRequest struct {
	Reason string `json:"reason"`
}

// entryResponse describes the ledger row a change wrote, or the stored row on a replay.
type entryResponse struct {
	EntryID  string `json:"entry_id"`
	Type     string `json:"type"`
	Amount   int    `json:"amount"`
	Balance  int    `json:"balance"`
	Replayed bool   `json:"replayed"`
}

// PostPoints handles POST /api/v1/family/members/{id}/points. The type is bonus, penalty or
// adjustment. The Idempotency-Key header is required.
func (h *Handlers) PostPoints(w http.ResponseWriter, r *http.Request) {
	familyID, userID, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || utf8.RuneCountInString(key) > maxIdemKey {
		invalid(w, "Idempotency-Key", "required, up to 100 characters")
		return
	}
	var req pointsRequest
	if !decodeReward(w, r, &req) {
		return
	}
	kind, ok := parseKind(req.Type)
	if !ok {
		invalid(w, "type", "must be bonus, penalty or adjustment")
		return
	}
	reason, ok := cleanReason(req.Reason)
	if !ok {
		invalid(w, "reason", "required, up to 200 characters")
		return
	}
	if !validPoints(kind, req.Points) {
		invalid(w, "points", "bonus and penalty must be 1 to 100000; adjustment must be non-zero and within 100000 either way")
		return
	}

	res, err := PostManual(r.Context(), h.db, h.clock.Now(), Manual{
		FamilyID:       familyID,
		MemberID:       r.PathValue("id"),
		ActorUserID:    userID,
		ActorMemberID:  memberID,
		Type:           kind,
		Points:         req.Points,
		Reason:         reason,
		IdempotencyKey: key,
	})
	switch {
	case errors.Is(err, ErrMemberNotFound):
		web.RespondError(w, http.StatusNotFound, "member_not_found", "Member not found", nil)
		return
	case errors.Is(err, ErrNothingToPenalise):
		web.RespondError(w, http.StatusConflict, "nothing_to_penalise",
			"The balance is zero, so there is nothing to take", nil)
		return
	case errors.Is(err, ErrIdempotencyConflict):
		web.RespondError(w, http.StatusConflict, "idempotency_conflict",
			"The Idempotency-Key was used for a different change", nil)
		return
	case err != nil:
		h.internalError(w, "post points", err)
		return
	}

	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	web.RespondJSON(w, status, entryResponse{
		EntryID:  res.EntryID,
		Type:     string(res.Type),
		Amount:   res.Amount,
		Balance:  res.Balance,
		Replayed: res.Replayed,
	})
}

// ReversePenaltyEntry handles POST /api/v1/points/entries/{id}/reverse. It undoes a penalty, once.
func (h *Handlers) ReversePenaltyEntry(w http.ResponseWriter, r *http.Request) {
	familyID, userID, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	var req reverseRequest
	if !decodeReward(w, r, &req) {
		return
	}
	reason, ok := cleanReason(req.Reason)
	if !ok {
		invalid(w, "reason", "required, up to 200 characters")
		return
	}

	res, err := ReversePenalty(r.Context(), h.db, h.clock.Now(), familyID, r.PathValue("id"), userID, memberID, reason)
	switch {
	case errors.Is(err, ErrEntryNotFound):
		web.RespondError(w, http.StatusNotFound, "entry_not_found", "Ledger entry not found", nil)
		return
	case errors.Is(err, ErrNotPenalty):
		web.RespondError(w, http.StatusConflict, "not_a_penalty", "Only a penalty can be reversed", nil)
		return
	case errors.Is(err, ErrAlreadyReversed):
		web.RespondError(w, http.StatusConflict, "already_reversed", "This penalty was already reversed", nil)
		return
	case err != nil:
		h.internalError(w, "reverse penalty", err)
		return
	}
	web.RespondJSON(w, http.StatusCreated, entryResponse{
		EntryID: res.EntryID,
		Type:    string(res.Type),
		Amount:  res.Amount,
		Balance: res.Balance,
	})
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

// parseKind maps the request type to a ledger type.
func parseKind(s string) (EntryType, bool) {
	switch s {
	case "bonus":
		return Bonus, true
	case "penalty":
		return Penalty, true
	case "adjustment":
		return Adjustment, true
	}
	return "", false
}

// validPoints checks the magnitude for the type. A bonus and a penalty are positive; an
// adjustment is signed and non-zero.
func validPoints(kind EntryType, points int) bool {
	if kind == Adjustment {
		return points != 0 && points >= -maxPoints && points <= maxPoints
	}
	return points >= 1 && points <= maxPoints
}

// cleanReason trims the reason and checks its length.
func cleanReason(s string) (string, bool) {
	s = strings.TrimSpace(s)
	return s, s != "" && utf8.RuneCountInString(s) <= maxReason
}

// decodeReward decodes a bounded JSON body and rejects unknown fields. It writes a 400 response
// and returns false on failure.
func decodeReward(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRewardBody))
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
	h.logger.Error("rewards: "+what, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}
