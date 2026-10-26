package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

// The intervals are variables so the tests can shorten them.
var (
	// streamPollInterval is how often a stream checks the database for writes.
	streamPollInterval = 2 * time.Second
	// streamHeartbeatInterval keeps an idle stream open through proxies. It must stay below the Nginx read timeout.
	streamHeartbeatInterval = 25 * time.Second
)

// streamRetryMillis is how long the browser waits before it reconnects after a dropped stream.
const streamRetryMillis = 3000

// inboxState is what a stream compares to decide whether the member's inbox changed. The stream sends no
// notification data; it only says "changed", and the browser reloads the list through the normal endpoint.
type inboxState struct {
	total  int64
	unread int64
	maxRow int64
}

// Stream sends a "changed" server-sent event whenever the signed-in member's inbox changes, so the bell updates
// without a page refresh. Each tick reads the inbox fingerprint through the connection pool and sends an event only
// when it moved. The stream holds no database connection between ticks, so a backup restore can still replace the
// database file on Windows. The event carries no data; the list endpoint stays the only place that returns
// notifications.
func (h *Handlers) Stream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		web.RespondError(w, http.StatusInternalServerError, "streaming_unsupported", "Streaming is not supported", nil)
		return
	}

	state, err := h.inboxStateOf(ctx, memberID)
	if err != nil {
		h.internalError(w, "notification stream: read inbox", err)
		return
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: %d\n\n", streamRetryMillis)
	flusher.Flush()

	poll := time.NewTicker(streamPollInterval)
	defer poll.Stop()
	beat := time.NewTicker(streamHeartbeatInterval)
	defer beat.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-beat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()

		case <-poll.C:
			latest, err := h.inboxStateOf(ctx, memberID)
			if err != nil {
				h.logger.Warn("notification stream: read inbox", slog.String("error", err.Error()))
				return
			}
			if latest == state {
				continue
			}
			state = latest
			if _, err := fmt.Fprint(w, "event: changed\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// inboxStateOf reads the counts that change when the member's inbox changes. Rows are only ever added or deleted,
// so the largest rowid moves on every new notice and the totals move on clears and reads.
func (h *Handlers) inboxStateOf(ctx context.Context, memberID string) (inboxState, error) {
	var s inboxState
	err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN read_at IS NULL THEN 1 ELSE 0 END), 0),
		        COALESCE(MAX(rowid), 0)
		 FROM notifications WHERE recipient_id = ?`,
		memberID,
	).Scan(&s.total, &s.unread, &s.maxRow)
	if errors.Is(err, sql.ErrNoRows) {
		return inboxState{}, nil
	}
	return s, err
}
