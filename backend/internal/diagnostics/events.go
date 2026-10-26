package diagnostics

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

// MaxEvents is how many diagnostics events are kept. Older rows are deleted on each insert.
const MaxEvents = 100

// Event kinds stored in diagnostic_events.
const (
	KindServerError  = "server_error"
	KindCalendarSync = "calendar_sync"
	KindEmail        = "email"
)

// Event is one recent failure shown on the diagnostics page. Code is a fixed short label, never error text.
type Event struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Code      string    `json:"code"`
	Status    *int      `json:"status"`
	Path      *string   `json:"path"`
	CreatedAt time.Time `json:"created_at"`
}

// Record stores one event for the family (or for the whole server when familyID is nil) and keeps only
// the newest MaxEvents rows.
func Record(ctx context.Context, db *sql.DB, familyID *string, kind, code string, status *int, path string, at time.Time) error {
	id, err := newID()
	if err != nil {
		return err
	}
	var pathArg any
	if path != "" {
		pathArg = path
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO diagnostic_events (id, family_id, kind, code, status, path, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, familyID, kind, code, status, pathArg, at.UTC()); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`DELETE FROM diagnostic_events WHERE rowid NOT IN (
		     SELECT rowid FROM diagnostic_events ORDER BY created_at DESC, rowid DESC LIMIT ?)`, MaxEvents)
	return err
}

// RecordServerErrorEvent counts a 5xx response and stores it in the event list. The request path is kept, the query string is not.
func RecordServerErrorEvent(ctx context.Context, db *sql.DB, at time.Time, path string, status int) error {
	RecordServerError(at)
	return Record(ctx, db, nil, KindServerError, "http_"+strconv.Itoa(status), &status, path, at)
}

// ListEvents returns the family's events and the server-wide server errors, newest first.
func (h *Handlers) ListEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	familyID, err := familyOf(ctx, h.db, memberID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, kind, code, status, path, created_at FROM diagnostic_events
		  WHERE family_id = ? OR family_id IS NULL
		  ORDER BY created_at DESC, rowid DESC LIMIT ?`, familyID, MaxEvents)
	if err != nil {
		h.internalError(w, "failed to list events", err)
		return
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var e Event
		var status sql.NullInt64
		var path sql.NullString
		if err := rows.Scan(&e.ID, &e.Kind, &e.Code, &status, &path, &e.CreatedAt); err != nil {
			h.internalError(w, "failed to read event", err)
			return
		}
		if status.Valid {
			s := int(status.Int64)
			e.Status = &s
		}
		if path.Valid {
			p := path.String
			e.Path = &p
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "failed to list events", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, map[string]any{"events": events, "max": MaxEvents})
}

// ClearEvents deletes the family's events and the server-wide ones, and resets the server error counter.
func (h *Handlers) ClearEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	familyID, err := familyOf(ctx, h.db, memberID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}
	userID, _ := permissions.GetUserID(ctx)

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to start clear", err)
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`DELETE FROM diagnostic_events WHERE family_id = ? OR family_id IS NULL`, familyID)
	if err != nil {
		h.internalError(w, "failed to clear events", err)
		return
	}
	deleted, _ := res.RowsAffected()
	if err := audit.Record(ctx, tx, familyID, userID, "diagnostics.events_cleared", "diagnostic_events", nil,
		map[string]any{"count": deleted}); err != nil {
		h.internalError(w, "failed to audit clear", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit clear", err)
		return
	}
	serverErrorCount.Store(0)
	serverErrorLastNano.Store(0)
	web.RespondJSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

