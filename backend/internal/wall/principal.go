package wall

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/platform/web"
)

// lastSeenInterval limits last-seen writes, so a tablet that polls does not write on every request.
const lastSeenInterval = time.Minute

// Device is the wall device that made a request. Only RequireDevice sets it.
type Device struct {
	ID       string
	FamilyID string
	Name     string
}

type deviceContextKey struct{}

// DeviceFromContext returns the device principal set by RequireDevice.
func DeviceFromContext(ctx context.Context) (Device, bool) {
	dev, ok := ctx.Value(deviceContextKey{}).(Device)
	return dev, ok
}

// RequireDevice authenticates a wall device from its credential cookie. It reads only that cookie, so a
// session cookie never authenticates a device, and the session middleware never reads the device cookie.
// Unpaired and revoked devices are refused the same way as unknown credentials.
func (h *Handlers) RequireDevice(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(deviceCookie)
		if err != nil {
			web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Device authentication required", nil)
			return
		}

		ctx := r.Context()
		var dev Device
		var lastSeen sql.NullTime
		err = h.db.QueryRowContext(ctx,
			`SELECT id, family_id, name, last_seen_at FROM wall_devices
			 WHERE credential_hash = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`,
			auth.HashToken(cookie.Value),
		).Scan(&dev.ID, &dev.FamilyID, &dev.Name, &lastSeen)
		if errors.Is(err, sql.ErrNoRows) {
			web.RespondError(w, http.StatusUnauthorized, "device_invalid", "This wall device is not paired or was revoked", nil)
			return
		}
		if err != nil {
			h.internalError(w, "failed to look up wall device", err)
			return
		}

		now := h.clk.Now().UTC()
		if !lastSeen.Valid || now.Sub(lastSeen.Time) >= lastSeenInterval {
			if _, err := h.db.ExecContext(ctx,
				`UPDATE wall_devices SET last_seen_at = ? WHERE id = ?`, now, dev.ID); err != nil {
				h.logger.Warn("failed to record wall device last seen", slog.String("error", err.Error()))
			}
		}

		next(w, r.WithContext(context.WithValue(ctx, deviceContextKey{}, dev)))
	}
}
