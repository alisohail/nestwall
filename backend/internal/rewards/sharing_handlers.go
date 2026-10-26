package rewards

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"nestwall/backend/internal/platform/web"
)

// shareRequest is the body of POST /api/v1/points/share. note_key is one of the preset notes and
// note_text is the child's own note (up to 200 characters). Send at most one of them; an empty
// value sends no note.
type shareRequest struct {
	ToMemberID string `json:"to_member_id"`
	Amount     int    `json:"amount"`
	NoteKey    string `json:"note_key"`
	NoteText   string `json:"note_text"`
}

// shareSettingsRequest is the body of PUT /api/v1/points/share/settings. It replaces the whole
// policy. The version comes from If-Match, not the body.
type shareSettingsRequest struct {
	Enabled         bool   `json:"enabled"`
	MinPerShare     int    `json:"min_per_share"`
	MaxPerShare     int    `json:"max_per_share"`
	DailyLimit      int    `json:"daily_limit"`
	MinBalance      int    `json:"min_balance"`
	ApprovalMode    string `json:"approval_mode"`
	ApprovalAbove   int    `json:"approval_above"`
	NotifyRecipient bool   `json:"notify_recipient"`
	NotifyParents   bool   `json:"notify_parents"`
}

// shareListPage is the body of GET /api/v1/point-shares.
type shareListPage struct {
	Shares []ShareListItem `json:"shares"`
}

// GetShare handles GET /api/v1/points/share. It returns the policy, the caller's balance and what
// the caller has sent today. It needs rewards.view. The ETag is the policy version.
func (h *Handlers) GetShare(w http.ResponseWriter, r *http.Request) {
	familyID, _, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	sum, err := ShareStatus(r.Context(), h.db, h.clock, familyID, memberID)
	if err != nil {
		h.internalError(w, "share status", err)
		return
	}
	setETag(w, sum.Version)
	web.RespondJSON(w, http.StatusOK, sum)
}

// PutShareSettings handles PUT /api/v1/points/share/settings. It needs rewards.manage. An If-Match
// header with the current version guards against a stale write.
func (h *Handlers) PutShareSettings(w http.ResponseWriter, r *http.Request) {
	familyID, userID, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, err := parseIfMatch(r)
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid If-Match header", nil)
		return
	}
	var req shareSettingsRequest
	if !decodeReward(w, r, &req) {
		return
	}
	s := ShareSettings{
		Enabled:         req.Enabled,
		MinPerShare:     req.MinPerShare,
		MaxPerShare:     req.MaxPerShare,
		DailyLimit:      req.DailyLimit,
		MinBalance:      req.MinBalance,
		ApprovalMode:    req.ApprovalMode,
		ApprovalAbove:   req.ApprovalAbove,
		NotifyRecipient: req.NotifyRecipient,
		NotifyParents:   req.NotifyParents,
	}
	if f := s.check(); f != nil {
		invalid(w, f.field, f.reason)
		return
	}

	saved, err := SaveShareSettings(r.Context(), h.db, h.clock, familyID, userID, s, expected)
	if errors.Is(err, errStaleVersion) {
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"The sharing settings changed since you loaded them. Reload and try again.",
			map[string]any{"version": saved.Version})
		return
	}
	if err != nil {
		h.internalError(w, "save share settings", err)
		return
	}
	setETag(w, saved.Version)
	web.RespondJSON(w, http.StatusOK, saved)
}

// PostShare handles POST /api/v1/points/share. A child sends points to a sibling. It needs an
// Idempotency-Key header. A new share returns 201; a replay returns 200 with the stored share. It
// needs points.share, so a parent gets 403.
func (h *Handlers) PostShare(w http.ResponseWriter, r *http.Request) {
	familyID, userID, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || utf8.RuneCountInString(key) > maxIdemKey {
		invalid(w, "Idempotency-Key", "required, up to 100 characters")
		return
	}
	var req shareRequest
	if !decodeReward(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ToMemberID) == "" {
		invalid(w, "to_member_id", "required")
		return
	}
	if req.Amount < 1 || req.Amount > maxPoints {
		invalid(w, "amount", "must be 1 to 100000")
		return
	}
	if req.NoteKey != "" && !isShareNote(req.NoteKey) {
		invalid(w, "note_key", "must be one of the preset notes")
		return
	}
	text := strings.TrimSpace(req.NoteText)
	if utf8.RuneCountInString(text) > maxShareNoteText {
		invalid(w, "note_text", "up to 200 characters")
		return
	}
	if req.NoteKey != "" && text != "" {
		invalid(w, "note_text", "send a preset note or your own text, not both")
		return
	}

	res, err := SendShare(r.Context(), h.db, h.clock, familyID, memberID, userID, ShareRequest{
		ToMemberID: req.ToMemberID,
		Amount:     req.Amount,
		NoteKey:    req.NoteKey,
		NoteText:   text,
	}, key)
	var amountErr *ShareAmountError
	var dailyErr *ShareDailyLimitError
	var minErr *ShareBelowMinimumError
	switch {
	case errors.Is(err, ErrSharingDisabled):
		web.RespondError(w, http.StatusConflict, "sharing_disabled", "Points sharing is turned off for this family", nil)
		return
	case errors.Is(err, ErrShareChildrenOnly):
		web.RespondError(w, http.StatusForbidden, "children_only", "Only a child can share points", nil)
		return
	case errors.Is(err, ErrShareRecipientMissing):
		web.RespondError(w, http.StatusNotFound, "member_not_found", "Member not found", nil)
		return
	case errors.Is(err, ErrShareRecipientInvalid):
		invalid(w, "to_member_id", "must be an active sibling child")
		return
	case errors.As(err, &amountErr):
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input",
			map[string]any{"field": "amount", "reason": fmt.Sprintf("must be %d to %d", amountErr.Min, amountErr.Max), "min": amountErr.Min, "max": amountErr.Max})
		return
	case errors.As(err, &dailyErr):
		web.RespondError(w, http.StatusConflict, "daily_limit_reached",
			"You have reached today's sharing limit", map[string]any{"remaining": dailyErr.Remaining})
		return
	case errors.As(err, &minErr):
		web.RespondError(w, http.StatusConflict, "below_minimum_balance",
			"This would leave you below the minimum balance",
			map[string]any{"balance": minErr.Balance, "min_balance": minErr.Minimum})
		return
	case errors.Is(err, ErrShareIdemConflict):
		web.RespondError(w, http.StatusConflict, "idempotency_conflict",
			"This Idempotency-Key was used for a different share", nil)
		return
	case err != nil:
		h.internalError(w, "send share", err)
		return
	}

	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	web.RespondJSON(w, status, res)
}

// ListPendingShares handles GET /api/v1/point-shares. It lists the shares that wait for a parent.
// It requires rewards.approve.
func (h *Handlers) ListPendingShares(w http.ResponseWriter, r *http.Request) {
	familyID, _, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	items, err := PendingShares(r.Context(), h.db, familyID)
	if err != nil {
		h.internalError(w, "list pending shares", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, shareListPage{Shares: items})
}

// ApprovePendingShare handles POST /api/v1/point-shares/{id}/approve. It requires rewards.approve.
// An If-Match header is optional.
func (h *Handlers) ApprovePendingShare(w http.ResponseWriter, r *http.Request) {
	familyID, userID, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, err := parseIfMatch(r)
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid If-Match header", nil)
		return
	}
	res, err := ApproveShare(r.Context(), h.db, h.clock, familyID, r.PathValue("id"), userID, memberID, expected)
	h.writeShareDecision(w, res, err)
}

// DeclinePendingShare handles POST /api/v1/point-shares/{id}/decline. The sender gets the points
// back. It requires rewards.approve. An If-Match header is optional.
func (h *Handlers) DeclinePendingShare(w http.ResponseWriter, r *http.Request) {
	familyID, userID, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	expected, err := parseIfMatch(r)
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid If-Match header", nil)
		return
	}
	res, err := DeclineShare(r.Context(), h.db, h.clock, familyID, r.PathValue("id"), userID, memberID, expected)
	h.writeShareDecision(w, res, err)
}

// writeShareDecision maps the result of approve or decline to a response.
func (h *Handlers) writeShareDecision(w http.ResponseWriter, res ShareResult, err error) {
	switch {
	case errors.Is(err, ErrShareNotFound):
		web.RespondError(w, http.StatusNotFound, "share_not_found", "Share not found", nil)
		return
	case errors.Is(err, errStaleVersion):
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"This changed since you loaded it. Reload and try again.", map[string]any{"version": res.Version})
		return
	case errors.Is(err, ErrNotPending):
		web.RespondError(w, http.StatusConflict, "not_pending", "This share is no longer pending", nil)
		return
	case err != nil:
		h.internalError(w, "decide share", err)
		return
	}
	setETag(w, res.Version)
	web.RespondJSON(w, http.StatusOK, res)
}
