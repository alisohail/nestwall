package audit

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

// Handlers handles audit log endpoints.
type Handlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(db *sql.DB, logger *slog.Logger) *Handlers {
	return &Handlers{
		db:     db,
		logger: logger,
	}
}

// AuditLogEntry represents an audit log entry.
type AuditLogEntry struct {
	ID         string         `json:"id"`
	FamilyID   string         `json:"family_id"`
	ActorID    string         `json:"actor_id"`
	Action     string         `json:"action"`
	TargetType *string        `json:"target_type,omitempty"`
	TargetID   *string        `json:"target_id,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// ListAuditLogsResponse represents the response for listing audit logs.
type ListAuditLogsResponse struct {
	Entries []AuditLogEntry `json:"entries"`
	Total   int             `json:"total"`
	Page    int             `json:"page"`
	Limit   int             `json:"limit"`
}

// ListAuditLogs handles GET /api/v1/audit/logs
func (h *Handlers) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		web.RespondError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", nil)
		return
	}

	familyMemberID, ok := permissions.GetFamilyMemberID(r.Context())
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	// Get family ID for the current member
	var familyID string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT family_id FROM family_members WHERE id = ?`,
		familyMemberID,
	).Scan(&familyID)
	if err != nil {
		h.logger.Error("failed to get family ID", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	// Parse pagination parameters
	page := 1
	limit := 50
	if p := r.URL.Query().Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	offset := (page - 1) * limit

	// Parse optional filters
	actionFilter := r.URL.Query().Get("action")
	targetTypeFilter := r.URL.Query().Get("target_type")
	targetIDFilter := r.URL.Query().Get("target_id")

	// Build query with filters
	whereClauses := []string{"al.family_id = ?"}
	args := []any{familyID}

	if actionFilter != "" {
		whereClauses = append(whereClauses, "al.action = ?")
		args = append(args, actionFilter)
	}
	if targetTypeFilter != "" {
		whereClauses = append(whereClauses, "al.target_type = ?")
		args = append(args, targetTypeFilter)
	}
	if targetIDFilter != "" {
		whereClauses = append(whereClauses, "al.target_id = ?")
		args = append(args, targetIDFilter)
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Get total count
	var total int
	countQuery := "SELECT COUNT(*) FROM audit_logs al " + whereSQL
	err = h.db.QueryRowContext(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		h.logger.Error("failed to count audit logs", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	// Get entries
	query := `SELECT al.id, al.family_id, al.actor_id, al.action, al.target_type, al.target_id, al.metadata, al.created_at 
		FROM audit_logs al ` + whereSQL + `
		ORDER BY al.created_at DESC 
		LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := h.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		h.logger.Error("failed to list audit logs", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	defer rows.Close()

	entries := []AuditLogEntry{}
	for rows.Next() {
		var entry AuditLogEntry
		var metadata []byte
		err := rows.Scan(&entry.ID, &entry.FamilyID, &entry.ActorID, &entry.Action, &entry.TargetType, &entry.TargetID, &metadata, &entry.CreatedAt)
		if err != nil {
			h.logger.Error("failed to scan audit log entry", slog.String("error", err.Error()))
			web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
			return
		}
		if metadata != nil {
			var metaMap map[string]any
			if err := json.Unmarshal(metadata, &metaMap); err == nil {
				entry.Metadata = metaMap
			}
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		h.logger.Error("failed to read audit logs", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	resp := ListAuditLogsResponse{
		Entries: entries,
		Total:   total,
		Page:    page,
		Limit:   limit,
	}

	web.RespondJSON(w, http.StatusOK, resp)
}
