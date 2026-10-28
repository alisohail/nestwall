package family

import (
	"net/http"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

// UpdateTimeFormatRequest is the body of PUT /api/v1/family/time-format.
type UpdateTimeFormatRequest struct {
	TimeFormat string `json:"time_format"`
}

// UpdateTimeFormat handles PUT /api/v1/family/time-format. It sets the clock format ("12h" or "24h")
// that every member and the wall display use. Permission (settings.manage) is enforced by the route.
func (h *Handlers) UpdateTimeFormat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	var req UpdateTimeFormatRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.TimeFormat != "12h" && req.TimeFormat != "24h" {
		web.RespondError(w, http.StatusBadRequest, "invalid_time_format", "Time format must be 12h or 24h", nil)
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
		`UPDATE families SET time_format = ?, updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now') WHERE id = ?`,
		req.TimeFormat, familyID); err != nil {
		h.internalError(w, "failed to save time format", err)
		return
	}

	meta := map[string]any{"time_format": req.TimeFormat}
	if err := audit.Record(ctx, tx, familyID, actorUserID, "family.time_format_updated", "family", &familyID, meta); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, UpdateTimeFormatRequest{TimeFormat: req.TimeFormat})
}
