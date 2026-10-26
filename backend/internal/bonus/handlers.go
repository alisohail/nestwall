package bonus

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

const maxBody = 16 << 10

// Handlers serves the bonus task endpoints. Publishing and deciding need bonus.manage. Claiming,
// releasing and completing need bonus.grab, and a parent is refused there with children_only.
// The list needs one of the two.
type Handlers struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
}

// NewHandlers returns the bonus task handlers.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger}
}

type decisionBody struct {
	DecisionNote string `json:"decision_note"`
}

// CreateTask handles POST /api/v1/bonus-tasks. It returns 201 with the task.
func (h *Handlers) CreateTask(w http.ResponseWriter, r *http.Request) {
	a, ok := h.actor(w, r)
	if !ok {
		return
	}
	var in PublishInput
	if !decode(w, r, &in) {
		return
	}
	task, err := Publish(r.Context(), h.db, h.clock, a, in)
	if h.failed(w, "publish bonus task", err) {
		return
	}
	setETag(w, task.Version)
	web.RespondJSON(w, http.StatusCreated, task)
}

// ListTasks handles GET /api/v1/bonus-tasks?scope=today|scheduled|past. The scope defaults to today. A
// parent gets every task in the scope with its claims. A child gets the tasks offered to them today, with
// their own claim, and may not ask for another scope.
func (h *Handlers) ListTasks(w http.ResponseWriter, r *http.Request) {
	a, ok := h.actor(w, r)
	if !ok {
		return
	}
	scope, ok := parseScope(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	policy := permissions.NewPolicy(h.db)
	manage, err := policy.Check(ctx, a.MemberID, permissions.PermBonusManage)
	if err != nil {
		h.internalError(w, "check bonus.manage", err)
		return
	}
	parent := manage.Allowed
	if !parent {
		grab, err := policy.Check(ctx, a.MemberID, permissions.PermBonusGrab)
		if err != nil {
			h.internalError(w, "check bonus.grab", err)
			return
		}
		if !grab.Allowed {
			web.RespondError(w, http.StatusForbidden, "forbidden", "You cannot view bonus tasks", nil)
			return
		}
		if scope != ScopeToday {
			web.RespondError(w, http.StatusForbidden, "forbidden", "Children can only list today's bonus tasks", nil)
			return
		}
	}
	tasks, err := List(ctx, h.db, h.clock, a, parent, scope)
	if h.failed(w, "list bonus tasks", err) {
		return
	}
	web.RespondJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

// EditTask handles PATCH /api/v1/bonus-tasks/{id}. The task version is sent as If-Match. It returns 200
// with the task.
func (h *Handlers) EditTask(w http.ResponseWriter, r *http.Request) {
	a, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, ok := expectedVersion(w, r)
	if !ok {
		return
	}
	var in EditInput
	if !decode(w, r, &in) {
		return
	}
	task, err := Edit(r.Context(), h.db, h.clock, a, r.PathValue("id"), in, expected)
	if h.failed(w, "edit bonus task", err) {
		return
	}
	setETag(w, task.Version)
	web.RespondJSON(w, http.StatusOK, task)
}

// CancelTask handles POST /api/v1/bonus-tasks/{id}/cancel. The task version is sent as If-Match. It
// returns 200 with the task. A task with a claim cannot be cancelled.
func (h *Handlers) CancelTask(w http.ResponseWriter, r *http.Request) {
	a, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, ok := expectedVersion(w, r)
	if !ok {
		return
	}
	task, err := Cancel(r.Context(), h.db, h.clock, a, r.PathValue("id"), expected)
	if h.failed(w, "cancel bonus task", err) {
		return
	}
	setETag(w, task.Version)
	web.RespondJSON(w, http.StatusOK, task)
}

// UnassignClaim handles POST /api/v1/bonus-claims/{id}/unassign. The claim version is sent as If-Match.
// It returns 200 with the claim. Nothing is posted to the ledger.
func (h *Handlers) UnassignClaim(w http.ResponseWriter, r *http.Request) {
	a, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, ok := expectedVersion(w, r)
	if !ok {
		return
	}
	c, err := Unassign(r.Context(), h.db, h.clock, a, r.PathValue("id"), expected)
	if h.failed(w, "unassign bonus claim", err) {
		return
	}
	setETag(w, c.Version)
	web.RespondJSON(w, http.StatusOK, c)
}

// parseScope reads the scope query parameter. It defaults to today. It writes a 400 response and returns
// ok=false for an unknown scope.
func parseScope(w http.ResponseWriter, r *http.Request) (Scope, bool) {
	switch raw := strings.TrimSpace(r.URL.Query().Get("scope")); raw {
	case "":
		return ScopeToday, true
	case string(ScopeToday), string(ScopeScheduled), string(ScopePast):
		return Scope(raw), true
	default:
		invalid(w, "scope", "invalid")
		return "", false
	}
}

// ClaimTask handles POST /api/v1/bonus-tasks/{id}/claim. It returns 201 for a new claim and 200 when
// the child already held the task.
func (h *Handlers) ClaimTask(w http.ResponseWriter, r *http.Request) {
	a, ok := h.actor(w, r)
	if !ok {
		return
	}
	res, err := Grab(r.Context(), h.db, h.clock, a, r.PathValue("id"))
	if h.failed(w, "claim bonus task", err) {
		return
	}
	setETag(w, res.Version)
	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	web.RespondJSON(w, status, res)
}

// ReleaseTask handles POST /api/v1/bonus-tasks/{id}/release. The claim version is sent as If-Match.
func (h *Handlers) ReleaseTask(w http.ResponseWriter, r *http.Request) {
	a, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, ok := expectedVersion(w, r)
	if !ok {
		return
	}
	c, err := Release(r.Context(), h.db, h.clock, a, r.PathValue("id"), expected)
	if h.failed(w, "release bonus task", err) {
		return
	}
	setETag(w, c.Version)
	web.RespondJSON(w, http.StatusOK, c)
}

// CompleteTask handles POST /api/v1/bonus-tasks/{id}/complete. The claim version is sent as If-Match.
func (h *Handlers) CompleteTask(w http.ResponseWriter, r *http.Request) {
	a, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, ok := expectedVersion(w, r)
	if !ok {
		return
	}
	c, err := Complete(r.Context(), h.db, h.clock, a, r.PathValue("id"), expected)
	if h.failed(w, "complete bonus task", err) {
		return
	}
	setETag(w, c.Version)
	web.RespondJSON(w, http.StatusOK, c)
}

// ApproveClaim handles POST /api/v1/bonus-claims/{id}/approve. It returns 200. A repeat returns the
// claim with replayed set and posts nothing.
func (h *Handlers) ApproveClaim(w http.ResponseWriter, r *http.Request) {
	a, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, ok := expectedVersion(w, r)
	if !ok {
		return
	}
	res, err := Approve(r.Context(), h.db, h.clock, a, r.PathValue("id"), expected)
	if h.failed(w, "approve bonus claim", err) {
		return
	}
	setETag(w, res.Version)
	web.RespondJSON(w, http.StatusOK, res)
}

// RejectClaim handles POST /api/v1/bonus-claims/{id}/reject. The body may carry a decision_note. A
// repeat returns the claim with replayed set.
func (h *Handlers) RejectClaim(w http.ResponseWriter, r *http.Request) {
	a, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, ok := expectedVersion(w, r)
	if !ok {
		return
	}
	var body decisionBody
	if !decodeOptional(w, r, &body) {
		return
	}
	res, err := Reject(r.Context(), h.db, h.clock, a, r.PathValue("id"), expected, body.DecisionNote)
	if h.failed(w, "reject bonus claim", err) {
		return
	}
	setETag(w, res.Version)
	web.RespondJSON(w, http.StatusOK, res)
}

// failed writes the error response for err and reports true. It reports false when err is nil.
func (h *Handlers) failed(w http.ResponseWriter, what string, err error) bool {
	if err == nil {
		return false
	}
	var (
		field *FieldError
		stale *StaleError
	)
	switch {
	case errors.Is(err, ErrNotFound):
		web.RespondError(w, http.StatusNotFound, "not_found", "Not found", nil)
	case errors.Is(err, ErrChildrenOnly):
		web.RespondError(w, http.StatusForbidden, "children_only", "Only a child can do this", nil)
	case errors.Is(err, ErrTaken):
		web.RespondError(w, http.StatusConflict, "taken", "Another child already has this task", nil)
	case errors.Is(err, ErrNotOpen):
		web.RespondError(w, http.StatusConflict, "not_open", "This task is not open yet", nil)
	case errors.Is(err, ErrExpired):
		web.RespondError(w, http.StatusConflict, "expired", "This task has ended", nil)
	case errors.Is(err, ErrCancelled):
		web.RespondError(w, http.StatusConflict, "cancelled", "This task has been cancelled", nil)
	case errors.Is(err, ErrHasClaims):
		web.RespondError(w, http.StatusConflict, "has_claims", "A child has already claimed this task", nil)
	case errors.Is(err, ErrWrongState):
		web.RespondError(w, http.StatusConflict, "wrong_state", "This action does not apply to the current state", nil)
	case errors.As(err, &stale):
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"This changed since you loaded it. Reload and try again.",
			map[string]any{"version": stale.Current})
	case errors.As(err, &field):
		invalid(w, field.Field, field.Reason)
	default:
		h.internalError(w, what, err)
	}
	return true
}

// actor returns the signed-in member's family and IDs. It writes the error response and returns
// ok=false when the request may not continue.
func (h *Handlers) actor(w http.ResponseWriter, r *http.Request) (Actor, bool) {
	ctx := r.Context()
	memberID, okMember := permissions.GetFamilyMemberID(ctx)
	userID, okUser := permissions.GetUserID(ctx)
	if !okMember || !okUser {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return Actor{}, false
	}
	var familyID string
	err := h.db.QueryRowContext(ctx,
		`SELECT family_id FROM family_members WHERE id = ? AND status = 'active'`, memberID,
	).Scan(&familyID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No active family membership", nil)
		return Actor{}, false
	}
	if err != nil {
		h.internalError(w, "load membership", err)
		return Actor{}, false
	}
	return Actor{FamilyID: familyID, UserID: userID, MemberID: memberID}, true
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

// setETag sets the ETag header to the quoted version of the task or claim.
func setETag(w http.ResponseWriter, version int) {
	w.Header().Set("ETag", `"`+strconv.Itoa(version)+`"`)
}

// decode reads a bounded JSON body and rejects unknown fields. It writes a 400 response and returns
// false on failure.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := decodeBody(w, r, v); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return false
	}
	return true
}

// decodeOptional is like decode, but an empty body leaves v unchanged.
func decodeOptional(w http.ResponseWriter, r *http.Request, v any) bool {
	err := decodeBody(w, r, v)
	if err != nil && !errors.Is(err, io.EOF) {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return false
	}
	return true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// invalid writes a 400 validation_failed response with the field and reason.
func invalid(w http.ResponseWriter, field, reason string) {
	web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input",
		map[string]any{"field": field, "reason": reason})
}

// internalError logs the cause and writes a generic 500 response.
func (h *Handlers) internalError(w http.ResponseWriter, what string, err error) {
	h.logger.Error("bonus: "+what, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}
