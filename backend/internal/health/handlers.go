package health

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

// Handler holds dependencies for health check handlers.
type Handler struct {
	clock clock.Clock
	pool  *sql.DB
}

// NewHandler creates a new Handler.
func NewHandler(c clock.Clock, pool *sql.DB) *Handler {
	return &Handler{clock: c, pool: pool}
}

// Live handles the liveness check.
func (h *Handler) Live(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{
		"status": "UP",
		"time":   h.clock.Now().Format("2006-01-02T15:04:05Z07:00"),
	}
	web.RespondJSON(w, http.StatusOK, response)
}

// Ready handles the readiness check.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.pool.PingContext(ctx); err != nil {
		web.RespondError(w, http.StatusServiceUnavailable, "DB_CONNECTION_FAILED", "Database connection failed", nil)
		return
	}

	web.RespondJSON(w, http.StatusOK, map[string]string{"status": "READY"})
}
