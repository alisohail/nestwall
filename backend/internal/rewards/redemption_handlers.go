package rewards

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"nestwall/backend/internal/platform/web"
)

// rejectRequest is the body of POST /api/v1/redemptions/{id}/reject.
type rejectRequest struct {
	Reason string `json:"reason"`
}

// RedeemReward handles POST /api/v1/rewards/{id}/redeem. It needs an Idempotency-Key header.
// A new request returns 201. A replay returns 200 with the stored request. It requires
// rewards.redeem.
func (h *Handlers) RedeemReward(w http.ResponseWriter, r *http.Request) {
	familyID, userID, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || utf8.RuneCountInString(key) > maxIdemKey {
		invalid(w, "Idempotency-Key", "required, up to 100 characters")
		return
	}

	res, err := Redeem(r.Context(), h.db, h.clock, familyID, memberID, userID, r.PathValue("id"), key)
	var short *InsufficientPointsError
	switch {
	case errors.Is(err, ErrRewardNotFound):
		web.RespondError(w, http.StatusNotFound, "reward_not_found", "Reward not found", nil)
		return
	case errors.Is(err, ErrRewardUnavailable):
		web.RespondError(w, http.StatusConflict, "reward_unavailable", "This reward is not available", nil)
		return
	case errors.Is(err, ErrOutsideWindow):
		web.RespondError(w, http.StatusConflict, "outside_window", "This reward is not available today", nil)
		return
	case errors.Is(err, ErrLimitReached):
		web.RespondError(w, http.StatusConflict, "limit_reached", "The redemption limit for this reward has been reached", nil)
		return
	case errors.As(err, &short):
		web.RespondError(w, http.StatusConflict, "insufficient_points", "Not enough points for this reward",
			map[string]any{"balance": short.Balance, "cost": short.Cost})
		return
	case errors.Is(err, ErrRedemptionIdemConflict):
		web.RespondError(w, http.StatusConflict, "idempotency_conflict",
			"This Idempotency-Key was used for a different reward", nil)
		return
	case err != nil:
		h.internalError(w, "redeem reward", err)
		return
	}

	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	setETag(w, res.Version)
	web.RespondJSON(w, status, res)
}

// ApproveRedemption handles POST /api/v1/redemptions/{id}/approve. It requires rewards.approve.
// An If-Match header guards against a stale write.
func (h *Handlers) ApproveRedemption(w http.ResponseWriter, r *http.Request) {
	familyID, userID, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, err := parseIfMatch(r)
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid If-Match header", nil)
		return
	}
	res, err := Approve(r.Context(), h.db, h.clock, familyID, r.PathValue("id"), userID, memberID, expected)
	h.writeDecision(w, res, err)
}

// RejectRedemption handles POST /api/v1/redemptions/{id}/reject. The body needs a reason and the
// points are given back. It requires rewards.approve. An If-Match header is optional.
func (h *Handlers) RejectRedemption(w http.ResponseWriter, r *http.Request) {
	familyID, userID, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, err := parseIfMatch(r)
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid If-Match header", nil)
		return
	}
	var req rejectRequest
	if !decodeReward(w, r, &req) {
		return
	}
	reason, ok := cleanReason(strings.TrimSpace(req.Reason))
	if !ok {
		invalid(w, "reason", "required, up to 200 characters")
		return
	}
	res, err := Reject(r.Context(), h.db, h.clock, familyID, r.PathValue("id"), userID, memberID, reason, expected)
	h.writeDecision(w, res, err)
}

// CancelRedemption handles POST /api/v1/redemptions/{id}/cancel. The requester withdraws a
// pending request and gets the points back. Another member's request is reported as not found.
// It requires rewards.redeem. An If-Match header is optional.
func (h *Handlers) CancelRedemption(w http.ResponseWriter, r *http.Request) {
	familyID, userID, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, err := parseIfMatch(r)
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid If-Match header", nil)
		return
	}
	res, err := Cancel(r.Context(), h.db, h.clock, familyID, r.PathValue("id"), userID, memberID, expected)
	h.writeDecision(w, res, err)
}

// writeDecision maps the result of approve, reject or cancel to a response.
func (h *Handlers) writeDecision(w http.ResponseWriter, res RedemptionResult, err error) {
	switch {
	case errors.Is(err, ErrRedemptionNotFound):
		web.RespondError(w, http.StatusNotFound, "redemption_not_found", "Redemption not found", nil)
		return
	case errors.Is(err, errStaleVersion):
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"This changed since you loaded it. Reload and try again.", map[string]any{"version": res.Version})
		return
	case errors.Is(err, ErrOwnRedemption):
		web.RespondError(w, http.StatusForbidden, "own_redemption", "You cannot decide your own redemption", nil)
		return
	case errors.Is(err, ErrNotPending):
		web.RespondError(w, http.StatusConflict, "not_pending", "This redemption is no longer pending", nil)
		return
	case err != nil:
		h.internalError(w, "decide redemption", err)
		return
	}
	setETag(w, res.Version)
	web.RespondJSON(w, http.StatusOK, res)
}
