package wall

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"nestwall/backend/internal/platform/web"
)

// The intervals are variables so the tests can shorten them.
var (
	// eventsPollInterval is how often a stream checks the database for writes. It keeps a completed task on
	// the wall within a few seconds.
	eventsPollInterval = 2 * time.Second
	// eventsHeartbeatInterval keeps an idle stream open through proxies. It must stay below the Nginx read timeout.
	eventsHeartbeatInterval = 25 * time.Second
)

// eventsRetryMillis is how long the browser waits before it reconnects after a dropped stream.
const eventsRetryMillis = 3000

// Events streams change signals to a paired wall device over server-sent events. The device reloads its
// summary on each "changed" message. Any committed write from another connection changes PRAGMA data_version,
// so the stream sees every write path without each handler publishing an event. The stream ends when the
// device is revoked, so the browser then gets a refusal on its next connect.
func (h *Handlers) Events(w http.ResponseWriter, r *http.Request) {
	dev, ok := DeviceFromContext(r.Context())
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Device authentication required", nil)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		web.RespondError(w, http.StatusInternalServerError, "streaming_unsupported", "Streaming is not supported", nil)
		return
	}

	ctx := r.Context()
	// The stream holds one connection for its lifetime, so its data_version is read from a connection that
	// makes no writes of its own.
	conn, err := h.db.Conn(ctx)
	if err != nil {
		h.internalError(w, "wall events: open connection", err)
		return
	}
	defer conn.Close()

	version, err := dataVersion(ctx, conn)
	if err != nil {
		h.internalError(w, "wall events: read data version", err)
		return
	}

	// An open stream is a live display, so each heartbeat also refreshes last_seen_at. Otherwise an idle
	// display would look offline in settings. RequireDevice has just recorded it, so the clock starts here,
	// before the client can see the stream open.
	lastSeen := h.clk.Now().UTC()

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: %d\n\n", eventsRetryMillis)
	flusher.Flush()

	poll := time.NewTicker(eventsPollInterval)
	defer poll.Stop()
	beat := time.NewTicker(eventsHeartbeatInterval)
	defer beat.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-beat.C:
			now := h.clk.Now().UTC()
			if now.Sub(lastSeen) >= lastSeenInterval {
				if _, err := h.db.ExecContext(ctx,
					`UPDATE wall_devices SET last_seen_at = ? WHERE id = ?`, now, dev.ID); err != nil {
					h.logger.Warn("wall events: record last seen", slog.String("error", err.Error()))
				} else {
					lastSeen = now
				}
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()

		case <-poll.C:
			next, err := dataVersion(ctx, conn)
			if err != nil {
				h.logger.Warn("wall events: read data version", slog.String("error", err.Error()))
				return
			}
			if next == version {
				continue
			}
			version = next

			active, err := h.deviceActive(ctx, dev.ID)
			if err != nil {
				h.logger.Warn("wall events: check device", slog.String("error", err.Error()))
				return
			}
			if !active {
				return
			}
			if _, err := fmt.Fprint(w, "event: changed\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// dataVersion returns SQLite's data_version for the connection. It changes when another connection commits.
func dataVersion(ctx context.Context, conn *sql.Conn) (int64, error) {
	var version int64
	err := conn.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&version)
	return version, err
}

// deviceActive reports whether the device is still paired and not revoked.
func (h *Handlers) deviceActive(ctx context.Context, deviceID string) (bool, error) {
	var one int
	err := h.db.QueryRowContext(ctx,
		`SELECT 1 FROM wall_devices WHERE id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`,
		deviceID,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
