package wall

import (
	"database/sql"
	"errors"
	"net/http"

	"nestwall/backend/internal/avatars"
	"nestwall/backend/internal/platform/web"
)

// ServeAvatar handles GET /api/v1/wall/avatars/{id}?size=96|256 for a paired device. The picture is
// returned only when the family shows pictures on the wall, the device shows pictures, and the picture
// belongs to an active member of the device's family. Anything else answers 404.
func (h *Handlers) ServeAvatar(w http.ResponseWriter, r *http.Request) {
	dev, ok := DeviceFromContext(r.Context())
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Device authentication required", nil)
		return
	}
	size, ok := avatars.ParseSize(r.URL.Query().Get("size"))
	if !ok {
		web.RespondError(w, http.StatusBadRequest, "invalid_size", "Size must be 96 or 256", nil)
		return
	}

	var avatarID string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT a.id
		   FROM member_avatars a
		   JOIN families f ON f.id = a.family_id AND f.show_avatars_on_wall = 1
		   JOIN family_members fm ON fm.id = a.family_member_id AND fm.family_id = a.family_id AND fm.status = 'active'
		   JOIN wall_devices d ON d.id = ? AND d.family_id = a.family_id AND d.show_pictures = 1
		  WHERE a.id = ? AND a.family_id = ?`,
		dev.ID, r.PathValue("id"), dev.FamilyID,
	).Scan(&avatarID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusNotFound, "not_found", "Picture not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to look up wall picture", err)
		return
	}

	avatars.ServeFile(w, h.logger, h.avatarDir, avatarID, size)
}
