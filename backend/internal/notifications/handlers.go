// Package notifications lists and marks the in-app notifications of the signed-in member.
// Each member sees only the notifications addressed to them.
package notifications

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

const listLimit = 50

// Handlers serves the notification endpoints.
type Handlers struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
	appURL string
}

// NewHandlers creates a new Handlers instance. appURL is the public address of the app, used for
// links in email previews.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger, appURL string) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger, appURL: strings.TrimRight(appURL, "/")}
}

// NotificationView is one notification as shown in the UI.
type NotificationView struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	SubjectMemberID *string    `json:"subject_member_id"`
	Body            string     `json:"body"`
	CreatedAt       time.Time  `json:"created_at"`
	ReadAt          *time.Time `json:"read_at"`
}

// ListResponse wraps the notification list, the unread count and whether the family has browser
// notifications switched on.
type ListResponse struct {
	Notifications  []NotificationView `json:"notifications"`
	UnreadCount    int                `json:"unread_count"`
	BrowserEnabled bool               `json:"browser_enabled"`
}

// List handles GET /api/v1/notifications. Unread notifications come first, newest first.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}

	rows, err := h.db.QueryContext(ctx,
		`SELECT id, kind, subject_member_id, body, created_at, read_at
		 FROM notifications
		 WHERE recipient_id = ?
		 ORDER BY (read_at IS NULL) DESC, created_at DESC
		 LIMIT ?`,
		memberID, listLimit,
	)
	if err != nil {
		h.internalError(w, "failed to list notifications", err)
		return
	}
	defer rows.Close()

	out := []NotificationView{}
	for rows.Next() {
		var n NotificationView
		var subject sql.NullString
		var readAt sql.NullTime
		if err := rows.Scan(&n.ID, &n.Kind, &subject, &n.Body, &n.CreatedAt, &readAt); err != nil {
			h.internalError(w, "failed to read notification", err)
			return
		}
		if subject.Valid {
			n.SubjectMemberID = &subject.String
		}
		if readAt.Valid {
			n.ReadAt = &readAt.Time
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "failed to list notifications", err)
		return
	}

	var unread int
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE recipient_id = ? AND read_at IS NULL`,
		memberID,
	).Scan(&unread); err != nil {
		h.internalError(w, "failed to count notifications", err)
		return
	}

	browser := notifypolicy.ShippedSettings().Browser
	var browserFlag sql.NullBool
	err = h.db.QueryRowContext(ctx,
		`SELECT ns.browser FROM family_members fm
		 LEFT JOIN notification_settings ns ON ns.family_id = fm.family_id
		 WHERE fm.id = ?`, memberID,
	).Scan(&browserFlag)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.internalError(w, "failed to load notification settings", err)
		return
	}
	if browserFlag.Valid {
		browser = browserFlag.Bool
	}

	web.RespondJSON(w, http.StatusOK, ListResponse{Notifications: out, UnreadCount: unread, BrowserEnabled: browser})
}

// MarkRead handles POST /api/v1/notifications/{id}/read. Reading twice keeps the first read time.
// A notification that is not addressed to the caller returns 404.
func (h *Handlers) MarkRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}

	res, err := h.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = COALESCE(read_at, ?) WHERE id = ? AND recipient_id = ?`,
		h.clock.Now(), r.PathValue("id"), memberID,
	)
	if err != nil {
		h.internalError(w, "failed to mark notification read", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		web.RespondError(w, http.StatusNotFound, "not_found", "Notification not found", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ClearAll handles DELETE /api/v1/notifications. It removes every notification addressed to the caller.
func (h *Handlers) ClearAll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}
	if _, err := h.db.ExecContext(ctx, `DELETE FROM notifications WHERE recipient_id = ?`, memberID); err != nil {
		h.internalError(w, "failed to clear notifications", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// internalError logs the error and writes a generic 500 response.
func (h *Handlers) internalError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
}
