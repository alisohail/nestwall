package health

import (
	"database/sql"
	"net/http"

	"nestwall/backend/internal/platform/clock"
)

// Routes sets up the health check routes.
func Routes(mux *http.ServeMux, c clock.Clock, pool *sql.DB) {
	h := NewHandler(c, pool)
	mux.HandleFunc("GET /api/v1/health/live", h.Live)
	mux.HandleFunc("GET /api/v1/health/ready", h.Ready)
}
