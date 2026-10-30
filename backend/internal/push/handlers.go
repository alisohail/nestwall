package push

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

const (
	maxSubscribeBody = 8 << 10
	maxEndpointLen   = 2048
	maxKeyLen        = 256
)

// Handlers serves the push endpoints. Every signed-in member manages their own subscriptions.
type Handlers struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
}

// NewHandlers creates the push handlers.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger}
}

// subscription is the part of a browser PushSubscription the server stores. Unknown fields such as
// expirationTime are ignored.
type subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// PublicKey handles GET /api/v1/push/public-key. The browser needs it to subscribe.
func (h *Handlers) PublicKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := permissions.GetFamilyMemberID(r.Context()); !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}
	keys, err := loadOrCreateKeys(r.Context(), h.db, h.clock.Now())
	if err != nil {
		h.internalError(w, "failed to load push keys", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, map[string]string{"public_key": keys.public})
}

// Subscribe handles POST /api/v1/push/subscribe. A browser that is already stored is moved to the
// signed-in member, so a shared device follows whoever signed in last.
func (h *Handlers) Subscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}
	var sub subscription
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSubscribeBody)).Decode(&sub); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return
	}
	if !strings.HasPrefix(sub.Endpoint, "https://") || len(sub.Endpoint) > maxEndpointLen {
		invalidField(w, "endpoint", "must be an https address")
		return
	}
	if sub.Keys.P256dh == "" || sub.Keys.Auth == "" || len(sub.Keys.P256dh) > maxKeyLen || len(sub.Keys.Auth) > maxKeyLen {
		invalidField(w, "keys", "p256dh and auth are required")
		return
	}
	_, err := h.db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (id, family_id, member_id, endpoint, p256dh, auth, created_at)
		 SELECT ?, family_id, id, ?, ?, ?, ? FROM family_members WHERE id = ?
		 ON CONFLICT(endpoint) DO UPDATE SET
		        family_id = excluded.family_id, member_id = excluded.member_id,
		        p256dh = excluded.p256dh, auth = excluded.auth`,
		ids.New(), sub.Endpoint, sub.Keys.P256dh, sub.Keys.Auth, h.clock.Now(), memberID,
	)
	if err != nil {
		h.internalError(w, "failed to save push subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unsubscribe handles POST /api/v1/push/unsubscribe. It removes one of the caller's own subscriptions.
// Removing an unknown endpoint is not an error.
func (h *Handlers) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSubscribeBody)).Decode(&body); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return
	}
	if _, err := h.db.ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE endpoint = ? AND member_id = ?`, body.Endpoint, memberID,
	); err != nil {
		h.internalError(w, "failed to remove push subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func invalidField(w http.ResponseWriter, field, reason string) {
	web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input",
		map[string]any{"field": field, "reason": reason})
}

func (h *Handlers) internalError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}
