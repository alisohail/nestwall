package family

import (
	"net/http"
	"strings"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

// UpdateFamilyRequest is the body of PUT /api/v1/family.
type UpdateFamilyRequest struct {
	FamilyName string `json:"family_name"`
}

// UpdateFamilyResponse returns the saved family name.
type UpdateFamilyResponse struct {
	FamilyName string `json:"family_name"`
}

// UpdateFamily handles PUT /api/v1/family. It renames the family.
// Permission (family.manage) is enforced by the route middleware.
func (h *Handlers) UpdateFamily(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actorMemberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	var req UpdateFamilyRequest
	if !decodeBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.FamilyName)
	if err := auth.ValidateName("family_name", name); err != nil {
		respondInvalid(w, err)
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

	if _, err := tx.ExecContext(ctx, `UPDATE families SET name = ? WHERE id = ?`, name, familyID); err != nil {
		h.internalError(w, "failed to rename family", err)
		return
	}

	meta := map[string]any{"family_name": name}
	if err := audit.Record(ctx, tx, familyID, actorUserID, "family.renamed", "family", &familyID, meta); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, UpdateFamilyResponse{FamilyName: name})
}
