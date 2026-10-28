package family

import (
	"net/http"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

// UpdateTimezoneRequest is the body of PUT /api/v1/family/timezone.
type UpdateTimezoneRequest struct {
	Timezone string `json:"timezone"`
}

// UpdateTimezone handles PUT /api/v1/family/timezone. It sets the IANA zone the family's dates, due
// times, weather and wall clock use. Permission (settings.manage) is enforced by the route.
func (h *Handlers) UpdateTimezone(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	var req UpdateTimezoneRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Timezone == "" || req.Timezone == "Local" || auth.ValidateTimezone(req.Timezone) != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_timezone", "Timezone must be a valid IANA zone", nil)
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	familyID, actorUserID, err := actorFamily(ctx, tx, actorMemberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE families SET timezone = ?, updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now') WHERE id = ?`,
		req.Timezone, familyID); err != nil {
		h.internalError(w, "failed to save timezone", err)
		return
	}

	meta := map[string]any{"timezone": req.Timezone}
	if err := audit.Record(ctx, tx, familyID, actorUserID, "family.timezone_updated", "family", &familyID, meta); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, UpdateTimezoneRequest{Timezone: req.Timezone})
}
