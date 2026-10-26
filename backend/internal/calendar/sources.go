package calendar

import (
	"context"
	"net/http"
	"strings"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

// maxSourceName matches the calendar_sources name column.
const maxSourceName = 100

// SourceResponse is one local calendar. Its events show this colour. EventCount counts the events
// made in the app that sit on it.
type SourceResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Colour     string `json:"colour"`
	EventCount int    `json:"event_count"`
}

// sourceColumns selects a local calendar with its manual event count. Imported copies are excluded.
const sourceColumns = `SELECT s.id, s.name, COALESCE(s.colour, '` + defaultLocalColour + `'),
	(SELECT COUNT(*) FROM calendar_events e WHERE e.family_id = s.family_id AND e.source_id = s.id AND e.import_id IS NULL)
	FROM calendar_sources s`

type updateSourceRequest struct {
	Name   string `json:"name"`
	Colour string `json:"colour"`
}

// ListSources returns the family's local calendars, oldest first. The default calendar is created
// on first use, so the list is never empty.
func (h *Handlers) ListSources(w http.ResponseWriter, r *http.Request) {
	familyID, _, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin list calendars", err)
		return
	}
	defer tx.Rollback()
	if _, err := ensureLocalSource(ctx, tx, familyID, h.clock.Now()); err != nil {
		h.internalError(w, "ensure default calendar", err)
		return
	}
	out, err := loadSources(ctx, tx, familyID)
	if err != nil {
		h.internalError(w, "load calendars", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit list calendars", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, map[string]any{"items": out})
}

// UpdateSource renames a local calendar and sets its colour. Another family's calendar is reported
// as not found.
func (h *Handlers) UpdateSource(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !isUUID(id) {
		notFound(w, "Calendar not found")
		return
	}
	var req updateSourceRequest
	if !decodeBody(w, r, &req) {
		return
	}
	name, err := requiredText("name", req.Name, maxSourceName)
	if err != nil {
		h.rejectInvalid(w, "calendar name", err)
		return
	}
	colour, err := sourceColour(req.Colour)
	if err != nil {
		h.rejectInvalid(w, "calendar colour", err)
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin update source", err)
		return
	}
	defer tx.Rollback()

	now := h.clock.Now()
	res, err := tx.ExecContext(ctx,
		`UPDATE calendar_sources SET name = ?, colour = ?, updated_at = ?
		 WHERE id = ? AND family_id = ? AND kind = 'local'`,
		name, colour, dbTime(now), id, familyID)
	if err != nil {
		h.internalError(w, "update source", err)
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		h.internalError(w, "update source rows", err)
		return
	}
	if n == 0 {
		notFound(w, "Calendar not found")
		return
	}
	details := map[string]any{"name": name, "colour": colour}
	if err := audit.Record(ctx, tx, familyID, userID, "calendar_source.updated", "calendar_source", &id, details); err != nil {
		h.internalError(w, "audit update source", err)
		return
	}
	out, err := loadSource(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "reload source", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit update source", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, out)
}

// CreateSource adds an empty manual calendar. The family can then assign events to it.
func (h *Handlers) CreateSource(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	var req updateSourceRequest
	if !decodeBody(w, r, &req) {
		return
	}
	name, err := requiredText("name", req.Name, maxSourceName)
	if err != nil {
		h.rejectInvalid(w, "calendar name", err)
		return
	}
	colour, err := sourceColour(req.Colour)
	if err != nil {
		h.rejectInvalid(w, "calendar colour", err)
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin create calendar", err)
		return
	}
	defer tx.Rollback()

	stamp := dbTime(h.clock.Now())
	id := ids.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO calendar_sources (id, family_id, kind, name, colour, created_at, updated_at)
		 VALUES (?, ?, 'local', ?, ?, ?, ?)`,
		id, familyID, name, colour, stamp, stamp); err != nil {
		h.internalError(w, "insert calendar", err)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "calendar_source.created", "calendar_source", &id,
		map[string]any{"name": name, "colour": colour}); err != nil {
		h.internalError(w, "audit create calendar", err)
		return
	}
	out, err := loadSource(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "reload calendar", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit create calendar", err)
		return
	}
	web.RespondJSON(w, http.StatusCreated, out)
}

// DeleteSource removes a local calendar and the events saved to it. Events brought in from an
// imported file keep their import: they move to the family's oldest other calendar. The last
// calendar cannot be deleted. Another family's calendar is reported as not found.
func (h *Handlers) DeleteSource(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !isUUID(id) {
		notFound(w, "Calendar not found")
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin delete calendar", err)
		return
	}
	defer tx.Rollback()

	exists, err := localSourceExists(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "find calendar", err)
		return
	}
	if !exists {
		notFound(w, "Calendar not found")
		return
	}
	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_sources WHERE family_id = ? AND kind = 'local'`,
		familyID).Scan(&count); err != nil {
		h.internalError(w, "count calendars", err)
		return
	}
	if count <= 1 {
		web.RespondError(w, http.StatusConflict, "last_calendar", "The last calendar cannot be deleted", nil)
		return
	}
	var fallback string
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM calendar_sources WHERE family_id = ? AND kind = 'local' AND id <> ?
		 ORDER BY created_at, id LIMIT 1`, familyID, id).Scan(&fallback); err != nil {
		h.internalError(w, "find fallback calendar", err)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE calendar_events SET source_id = ? WHERE family_id = ? AND source_id = ? AND import_id IS NOT NULL`,
		fallback, familyID, id); err != nil {
		h.internalError(w, "move imported events", err)
		return
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_events WHERE family_id = ? AND source_id = ?`, familyID, id)
	if err != nil {
		h.internalError(w, "delete calendar events", err)
		return
	}
	events, err := res.RowsAffected()
	if err != nil {
		h.internalError(w, "delete calendar event rows", err)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_sources WHERE id = ? AND family_id = ?`, id, familyID); err != nil {
		h.internalError(w, "delete calendar", err)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "calendar_source.deleted", "calendar_source", &id,
		map[string]any{"events": events}); err != nil {
		h.internalError(w, "audit delete calendar", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit delete calendar", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loadSource reads one of the family's local calendars.
func loadSource(ctx context.Context, q querier, familyID, id string) (SourceResponse, error) {
	var out SourceResponse
	err := q.QueryRowContext(ctx,
		sourceColumns+` WHERE s.id = ? AND s.family_id = ? AND s.kind = 'local'`,
		id, familyID).Scan(&out.ID, &out.Name, &out.Colour, &out.EventCount)
	return out, err
}

// loadSources reads the family's local calendars, oldest first.
func loadSources(ctx context.Context, q querier, familyID string) ([]SourceResponse, error) {
	rows, err := q.QueryContext(ctx,
		sourceColumns+` WHERE s.family_id = ? AND s.kind = 'local' ORDER BY s.created_at, s.id`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceResponse{}
	for rows.Next() {
		var s SourceResponse
		if err := rows.Scan(&s.ID, &s.Name, &s.Colour, &s.EventCount); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// sourceColour checks a calendar colour and returns it in upper case. A calendar always has one.
func sourceColour(raw string) (string, error) {
	c := strings.TrimSpace(raw)
	if !colourPattern.MatchString(c) {
		return "", fieldError("colour", "invalid")
	}
	return strings.ToUpper(c), nil
}
