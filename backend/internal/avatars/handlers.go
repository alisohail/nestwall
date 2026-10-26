package avatars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

const maxSettingsBody = 64 << 10

// Handlers serves profile picture endpoints. Files live in dir and are named by avatar ID.
type Handlers struct {
	db     *sql.DB
	dir    string
	logger *slog.Logger
	policy *permissions.Policy
}

// NewHandlers creates a new Handlers instance. dir is the avatars folder beside the database.
func NewHandlers(db *sql.DB, dir string, logger *slog.Logger) *Handlers {
	return &Handlers{
		db:     db,
		dir:    dir,
		logger: logger,
		policy: permissions.NewPolicy(db),
	}
}

// SettingsResponse is the Profile pictures card state for a family.
type SettingsResponse struct {
	MembersCanChangeAvatar bool `json:"members_can_change_avatar"`
	ShowAvatarsOnWall      bool `json:"show_avatars_on_wall"`
}

// UpdateSettingsRequest changes the Profile pictures card. Omitted fields keep their value.
type UpdateSettingsRequest struct {
	MembersCanChangeAvatar *bool `json:"members_can_change_avatar,omitempty"`
	ShowAvatarsOnWall      *bool `json:"show_avatars_on_wall,omitempty"`
}

// denial is a rejected request with its response code.
type denial struct {
	status  int
	code    string
	message string
}

// Upload handles POST /api/v1/family/members/{id}/avatar.
// The multipart field "file" holds the picture. Optional fields x, y and size give a square crop in pixels.
func (h *Handlers) Upload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actorID, actorUserID, targetID, ok := h.begin(w, r)
	if !ok {
		return
	}
	familyID, d, err := h.authorise(ctx, actorID, targetID)
	if err != nil {
		h.internalError(w, "failed to authorise avatar change", err)
		return
	}
	if d != nil {
		web.RespondError(w, d.status, d.code, d.message, nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes+64<<10)
	if err := r.ParseMultipartForm(MaxUploadBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			web.RespondError(w, http.StatusRequestEntityTooLarge, "image_too_large", "Picture must be 5 MB or smaller", nil)
			return
		}
		web.RespondError(w, http.StatusBadRequest, "invalid_form", "Upload must be multipart form data", nil)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "missing_file", "Choose a picture to upload", nil)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, MaxUploadBytes+1))
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_form", "Could not read the picture", nil)
		return
	}
	if len(data) > MaxUploadBytes {
		web.RespondError(w, http.StatusRequestEntityTooLarge, "image_too_large", "Picture must be 5 MB or smaller", nil)
		return
	}

	crop, err := parseCrop(r.FormValue("x"), r.FormValue("y"), r.FormValue("size"))
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_crop", "Crop values must be whole numbers", nil)
		return
	}

	processed, err := Process(data, crop)
	if err != nil {
		if status, code, message, known := rejection(err); known {
			web.RespondError(w, status, code, message, nil)
			return
		}
		h.internalError(w, "failed to process picture", err)
		return
	}

	avatarID := ids.New()
	if err := h.writeFiles(avatarID, processed); err != nil {
		h.internalError(w, "failed to write picture files", err)
		return
	}

	oldIDs, err := h.replaceRows(ctx, targetID, familyID, avatarID, actorID, actorUserID, "avatar.updated")
	if err != nil {
		h.removeFiles(avatarID)
		h.internalError(w, "failed to save picture", err)
		return
	}
	h.removeFiles(oldIDs...)

	web.RespondJSON(w, http.StatusOK, map[string]string{"avatar_id": avatarID})
}

// Delete handles DELETE /api/v1/family/members/{id}/avatar. The member then shows their letter again.
func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actorID, actorUserID, targetID, ok := h.begin(w, r)
	if !ok {
		return
	}
	familyID, d, err := h.authorise(ctx, actorID, targetID)
	if err != nil {
		h.internalError(w, "failed to authorise avatar removal", err)
		return
	}
	if d != nil {
		web.RespondError(w, d.status, d.code, d.message, nil)
		return
	}

	oldIDs, err := h.replaceRows(ctx, targetID, familyID, "", actorID, actorUserID, "avatar.removed")
	if err != nil {
		h.internalError(w, "failed to remove picture", err)
		return
	}
	h.removeFiles(oldIDs...)

	w.WriteHeader(http.StatusNoContent)
}

// Serve handles GET /api/v1/avatars/{id}?size=96|256. Only members of the same family can read a picture.
func (h *Handlers) Serve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actorID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	size, ok := ParseSize(r.URL.Query().Get("size"))
	if !ok {
		web.RespondError(w, http.StatusBadRequest, "invalid_size", "Size must be 96 or 256", nil)
		return
	}

	var avatarID string
	err := h.db.QueryRowContext(ctx,
		`SELECT a.id
		 FROM member_avatars a
		 JOIN family_members actor ON actor.family_id = a.family_id AND actor.id = ? AND actor.status = 'active'
		 WHERE a.id = ?`,
		actorID, r.PathValue("id"),
	).Scan(&avatarID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusNotFound, "not_found", "Picture not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to look up picture", err)
		return
	}

	ServeFile(w, h.logger, h.dir, avatarID, size)
}

// ServeFile writes the picture for an avatar at a size. Callers check access first. A missing file
// answers 404.
func ServeFile(w http.ResponseWriter, logger *slog.Logger, dir, avatarID string, size int) {
	f, err := os.Open(filepath.Join(dir, fileName(avatarID, size)))
	if errors.Is(err, os.ErrNotExist) {
		web.RespondError(w, http.StatusNotFound, "not_found", "Picture not found", nil)
		return
	}
	if err != nil {
		logger.Error("failed to open picture", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred", nil)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, f); err != nil {
		logger.Warn("failed to write picture", slog.String("error", err.Error()))
	}
}

// GetSettings handles GET /api/v1/family/avatar-settings.
func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actorID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	var resp SettingsResponse
	err := h.db.QueryRowContext(ctx,
		`SELECT f.members_can_change_avatar, f.show_avatars_on_wall
		 FROM families f
		 JOIN family_members fm ON fm.family_id = f.id
		 WHERE fm.id = ? AND fm.status = 'active'`,
		actorID,
	).Scan(&resp.MembersCanChangeAvatar, &resp.ShowAvatarsOnWall)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to read avatar settings", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, resp)
}

// UpdateSettings handles PUT /api/v1/family/avatar-settings.
func (h *Handlers) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actorID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	actorUserID, ok := permissions.GetUserID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	var req UpdateSettingsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingsBody)).Decode(&req); err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_json", "Invalid request body", nil)
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	var familyID string
	var cur SettingsResponse
	err = tx.QueryRowContext(ctx,
		`SELECT f.id, f.members_can_change_avatar, f.show_avatars_on_wall
		 FROM families f
		 JOIN family_members fm ON fm.family_id = f.id
		 WHERE fm.id = ? AND fm.status = 'active'`,
		actorID,
	).Scan(&familyID, &cur.MembersCanChangeAvatar, &cur.ShowAvatarsOnWall)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to read avatar settings", err)
		return
	}

	if req.MembersCanChangeAvatar != nil {
		cur.MembersCanChangeAvatar = *req.MembersCanChangeAvatar
	}
	if req.ShowAvatarsOnWall != nil {
		cur.ShowAvatarsOnWall = *req.ShowAvatarsOnWall
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE families SET members_can_change_avatar = ?, show_avatars_on_wall = ? WHERE id = ?`,
		cur.MembersCanChangeAvatar, cur.ShowAvatarsOnWall, familyID,
	); err != nil {
		h.internalError(w, "failed to update avatar settings", err)
		return
	}

	meta := map[string]any{
		"members_can_change_avatar": cur.MembersCanChangeAvatar,
		"show_avatars_on_wall":      cur.ShowAvatarsOnWall,
	}
	if err := audit.Record(ctx, tx, familyID, actorUserID, "avatar.settings_changed", "family", &familyID, meta); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, cur)
}

// begin reads the signed-in actor and the target member ID. It writes the error response when the actor is missing.
func (h *Handlers) begin(w http.ResponseWriter, r *http.Request) (actorID, actorUserID, targetID string, ok bool) {
	ctx := r.Context()
	actorID, ok = permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return "", "", "", false
	}
	actorUserID, ok = permissions.GetUserID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return "", "", "", false
	}
	return actorID, actorUserID, r.PathValue("id"), true
}

// authorise checks that the actor may change the target's picture and returns the family ID.
// A denial is returned when they may not. A member may change their own picture when the family allows it.
// Anyone else needs members.manage.
func (h *Handlers) authorise(ctx context.Context, actorID, targetID string) (string, *denial, error) {
	var familyID string
	var membersCanChange bool
	err := h.db.QueryRowContext(ctx,
		`SELECT target.family_id, f.members_can_change_avatar
		 FROM family_members target
		 JOIN family_members actor ON actor.family_id = target.family_id AND actor.id = ? AND actor.status = 'active'
		 JOIN families f ON f.id = target.family_id
		 WHERE target.id = ? AND target.status = 'active'`,
		actorID, targetID,
	).Scan(&familyID, &membersCanChange)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &denial{http.StatusNotFound, "not_found", "Member not found"}, nil
	}
	if err != nil {
		return "", nil, err
	}

	if actorID == targetID && membersCanChange {
		return familyID, nil, nil
	}

	res, err := h.policy.Check(ctx, actorID, permissions.PermMembersManage)
	if err != nil {
		return "", nil, err
	}
	if res.Allowed {
		return familyID, nil, nil
	}
	if actorID == targetID {
		return "", &denial{http.StatusForbidden, "avatar_changes_locked", "Picture changes are turned off for members"}, nil
	}
	return "", &denial{http.StatusForbidden, "forbidden", "You do not have permission to change this picture"}, nil
}

// replaceRows swaps the member's picture rows in one transaction and records the audit entry.
// newID is empty when the picture is removed. It returns the IDs of the rows it replaced.
func (h *Handlers) replaceRows(ctx context.Context, targetID, familyID, newID, actorID, actorUserID, action string) ([]string, error) {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id FROM member_avatars WHERE family_member_id = ?`, targetID)
	if err != nil {
		return nil, err
	}
	var oldIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		oldIDs = append(oldIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM member_avatars WHERE family_member_id = ?`, targetID); err != nil {
		return nil, err
	}
	if newID != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO member_avatars (id, family_id, family_member_id) VALUES (?, ?, ?)`,
			newID, familyID, targetID,
		); err != nil {
			return nil, err
		}
	}

	meta := map[string]any{"self": actorID == targetID}
	if err := audit.Record(ctx, tx, familyID, actorUserID, action, "family_member", &targetID, meta); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return oldIDs, nil
}

// writeFiles stores both sizes under the avatar ID. Each file is written to a temp name and then renamed.
func (h *Handlers) writeFiles(id string, p Processed) error {
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return err
	}
	files := []struct {
		size int
		data []byte
	}{{LargeSize, p.Large}, {SmallSize, p.Small}}

	for _, f := range files {
		if err := writeAtomic(filepath.Join(h.dir, fileName(id, f.size)), f.data); err != nil {
			h.removeFiles(id)
			return err
		}
	}
	return nil
}

// removeFiles deletes both sizes for each avatar ID. Missing files are ignored.
func (h *Handlers) removeFiles(avatarIDs ...string) {
	RemoveFiles(h.dir, h.logger, avatarIDs...)
}

// RemoveFiles deletes both sizes of each avatar ID from dir. Missing files are ignored.
func RemoveFiles(dir string, logger *slog.Logger, avatarIDs ...string) {
	for _, id := range avatarIDs {
		for _, size := range []int{LargeSize, SmallSize} {
			err := os.Remove(filepath.Join(dir, fileName(id, size)))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				logger.Warn("failed to remove picture file", slog.String("error", err.Error()))
			}
		}
	}
}

// internalError logs the error and writes a generic 500 response.
func (h *Handlers) internalError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred", nil)
}

// fileName returns the file name for an avatar at a size. The name uses the random avatar ID only.
func fileName(id string, size int) string {
	return fmt.Sprintf("%s-%d.jpg", id, size)
}

// writeAtomic writes data to a temp file in the same folder and renames it into place.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".avatar-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// parseCrop reads the optional crop fields. An empty size means no crop, so the centred square is used.
func parseCrop(xs, ys, sizes string) (Crop, error) {
	if sizes == "" {
		return Crop{}, nil
	}
	size, err := strconv.Atoi(sizes)
	if err != nil || size < 1 {
		return Crop{}, ErrInvalidCrop
	}
	x, y := 0, 0
	if xs != "" {
		if x, err = strconv.Atoi(xs); err != nil {
			return Crop{}, ErrInvalidCrop
		}
	}
	if ys != "" {
		if y, err = strconv.Atoi(ys); err != nil {
			return Crop{}, ErrInvalidCrop
		}
	}
	return Crop{X: x, Y: y, Size: size}, nil
}

// ParseSize maps the size query to a stored size. An empty value means the large size.
func ParseSize(s string) (int, bool) {
	switch s {
	case "", strconv.Itoa(LargeSize):
		return LargeSize, true
	case strconv.Itoa(SmallSize):
		return SmallSize, true
	}
	return 0, false
}

// rejection maps a processing error to a response. known is false for unexpected errors.
func rejection(err error) (status int, code, message string, known bool) {
	switch {
	case errors.Is(err, ErrUnsupportedType):
		return http.StatusBadRequest, "unsupported_type", "Use a JPG or PNG picture", true
	case errors.Is(err, ErrAnimated):
		return http.StatusBadRequest, "animated_image", "Animated pictures are not allowed", true
	case errors.Is(err, ErrDimensions):
		return http.StatusBadRequest, "image_dimensions", "Picture is too large in pixels", true
	case errors.Is(err, ErrInvalidImage):
		return http.StatusBadRequest, "invalid_image", "Picture could not be read", true
	case errors.Is(err, ErrInvalidCrop):
		return http.StatusBadRequest, "invalid_crop", "Crop is outside the picture", true
	}
	return 0, "", "", false
}
