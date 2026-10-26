package wall

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

// deviceOnlineWindow is how recent a heartbeat must be for a device to count as online. A live display
// beats every eventsHeartbeatInterval, so it stays inside the window.
const deviceOnlineWindow = 2 * time.Minute

type deviceResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GetDevice returns the calling device. Route it through RequireDevice.
func (h *Handlers) GetDevice(w http.ResponseWriter, r *http.Request) {
	dev, ok := DeviceFromContext(r.Context())
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Device authentication required", nil)
		return
	}
	web.RespondJSON(w, http.StatusOK, deviceResponse{ID: dev.ID, Name: dev.Name})
}

// RevokeDevice stops a wall device. Its credential is refused on the next request, and a pairing key that has
// not been used yet can no longer be redeemed. The row stays so the audit trail still names the device.
func (h *Handlers) RevokeDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}
	familyID, userID, err := actorFamily(ctx, h.db, memberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}
	deviceID := r.PathValue("id")
	now := h.clk.Now().UTC()

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	var name string
	err = tx.QueryRowContext(ctx,
		`SELECT name FROM wall_devices WHERE family_id = ? AND id = ? AND revoked_at IS NULL`,
		familyID, deviceID,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusNotFound, "not_found", "Wall device not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find wall device", err)
		return
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE wall_devices SET revoked_at = ?, updated_at = ? WHERE family_id = ? AND id = ?`,
		now, now, familyID, deviceID,
	); err != nil {
		h.internalError(w, "failed to revoke wall device", err)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "wall_device.revoked", "wall_device", &deviceID,
		map[string]any{"name": name}); err != nil {
		h.internalError(w, "failed to record audit event", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// deviceRow is one wall device as the settings screen reads it.
type deviceRow struct {
	ID         string
	Name       string
	PairedAt   sql.NullTime
	LastSeenAt sql.NullTime
}

const deviceColumns = `id, name, paired_at, last_seen_at`

func scanDevice(s interface{ Scan(dest ...any) error }) (deviceRow, error) {
	var d deviceRow
	err := s.Scan(&d.ID, &d.Name, &d.PairedAt, &d.LastSeenAt)
	return d, err
}

type deviceView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	PairedAt   *time.Time `json:"paired_at"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

// view derives the status: pending until the key is used, then online while heartbeats are recent.
func (d deviceRow) view(now time.Time) deviceView {
	v := deviceView{ID: d.ID, Name: d.Name}
	if d.PairedAt.Valid {
		paired := d.PairedAt.Time
		v.PairedAt = &paired
	}
	if d.LastSeenAt.Valid {
		seen := d.LastSeenAt.Time
		v.LastSeenAt = &seen
	}
	switch {
	case !d.PairedAt.Valid:
		v.Status = "pending"
	case d.LastSeenAt.Valid && now.Sub(d.LastSeenAt.Time) <= deviceOnlineWindow:
		v.Status = "online"
	default:
		v.Status = "offline"
	}
	return v
}

type listDevicesResponse struct {
	Devices []deviceView `json:"devices"`
}

// ListDevices returns the family's wall devices that are not revoked, with their status.
func (h *Handlers) ListDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	familyID, _, err := actorFamily(ctx, h.db, memberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}

	rows, err := h.db.QueryContext(ctx,
		`SELECT `+deviceColumns+` FROM wall_devices
		  WHERE family_id = ? AND revoked_at IS NULL
		  ORDER BY created_at, id`,
		familyID)
	if err != nil {
		h.internalError(w, "failed to list wall devices", err)
		return
	}
	defer rows.Close()

	now := h.clk.Now().UTC()
	resp := listDevicesResponse{Devices: []deviceView{}}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			h.internalError(w, "failed to read wall device", err)
			return
		}
		resp.Devices = append(resp.Devices, d.view(now))
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "failed to list wall devices", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, resp)
}

// updateDeviceRequest carries only the fields a parent changed. A nil field is left as it is.
type updateDeviceRequest struct {
	Name     *string `json:"name"`
	ExitCode *string `json:"exit_code"`
}

// UpdateDevice changes a wall device's name or exit code. The exit code is stored only as a hash and is
// never written to the audit trail.
func (h *Handlers) UpdateDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	var req updateDeviceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if n := utf8.RuneCountInString(name); n < 1 || n > maxNameRunes {
			respondInvalid(w, &auth.ValidationError{Field: "name", Reason: "invalid_length"})
			return
		}
		req.Name = &name
	}
	if req.ExitCode != nil && !exitCodePattern.MatchString(*req.ExitCode) {
		respondInvalid(w, &auth.ValidationError{Field: "exit_code", Reason: "invalid_format"})
		return
	}

	familyID, userID, err := actorFamily(ctx, h.db, memberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}
	deviceID := r.PathValue("id")
	now := h.clk.Now().UTC()

	var newExitHash any
	if req.ExitCode != nil {
		hash, err := auth.HashPassword(*req.ExitCode)
		if err != nil {
			h.internalError(w, "failed to hash exit code", err)
			return
		}
		newExitHash = hash
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	dev, err := scanDevice(tx.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM wall_devices WHERE family_id = ? AND id = ? AND revoked_at IS NULL`,
		familyID, deviceID))
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusNotFound, "not_found", "Wall device not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find wall device", err)
		return
	}

	var changed []string
	if req.Name != nil && *req.Name != dev.Name {
		dev.Name = *req.Name
		changed = append(changed, "name")
	}
	if req.ExitCode != nil {
		changed = append(changed, "exit_code")
	}
	if len(changed) > 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE wall_devices
			    SET name = ?, exit_code_hash = COALESCE(?, exit_code_hash), updated_at = ?
			  WHERE family_id = ? AND id = ?`,
			dev.Name, newExitHash, now,
			familyID, deviceID,
		); err != nil {
			h.internalError(w, "failed to update wall device", err)
			return
		}
		if err := audit.Record(ctx, tx, familyID, userID, "wall_device.updated", "wall_device", &deviceID,
			map[string]any{"name": dev.Name, "fields": changed}); err != nil {
			h.internalError(w, "failed to record audit event", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, dev.view(now))
}
