package calendar

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/diagnostics"
	"nestwall/backend/internal/platform/web"
)

// maxSyncPerPass caps how many linked calendars one scheduler pass refreshes, so a long list of slow
// sources cannot hold up the rest of the scheduled work. The others run on later passes.
const maxSyncPerPass = 10

// syncNowCooldown is the least time between two attempts on one linked calendar started by hand.
const syncNowCooldown = time.Minute

// errNotLinked means the import was uploaded as a file, so it has no link to refresh.
var errNotLinked = errors.New("import has no calendar link")

// errSyncTooSoon means the calendar was attempted within syncNowCooldown.
var errSyncTooSoon = errors.New("calendar was attempted too recently")

// respondFeedError answers a request whose calendar link could not be used. The message never
// includes the link.
func respondFeedError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidURL):
		web.RespondError(w, http.StatusBadRequest, "invalid_url", "Enter a full web address that starts with http:// or https://", nil)
	case errors.Is(err, errLinkNotAllowed):
		web.RespondError(w, http.StatusBadRequest, "link_not_allowed", "This address is not allowed for a calendar link", nil)
	case errors.Is(err, errFeedTooLarge):
		web.RespondError(w, http.StatusRequestEntityTooLarge, "calendar_too_large", "The calendar at this link is larger than 10 MB", nil)
	default:
		web.RespondError(w, http.StatusBadGateway, "calendar_unavailable", "The calendar at this link could not be fetched", nil)
	}
}

// feedErrorCode is the short code stored in last_sync_error when a refresh fails.
func feedErrorCode(err error) string {
	switch {
	case errors.Is(err, errInvalidURL):
		return "invalid_url"
	case errors.Is(err, errLinkNotAllowed):
		return "link_not_allowed"
	case errors.Is(err, errFeedTooLarge):
		return "too_large"
	case errors.Is(err, errInvalidICS):
		return "invalid_ics"
	case errors.Is(err, errTooManyEvents):
		return "too_many_events"
	}
	return "unavailable"
}

// syncImportFeed fetches a linked calendar and replaces its events with the new ones. When the
// fetch or the parse fails, or the calendar has no events, the import keeps its last good events
// and stores the reason in last_sync_error, so the next run can recover. It returns an error only
// when the database fails.
func (h *Handlers) syncImportFeed(ctx context.Context, familyID, importID string) error {
	var sealed, etag sql.NullString
	var createdBy string
	var parserVersion int
	err := h.db.QueryRowContext(ctx,
		`SELECT source_url_sealed, source_etag, created_by, parser_version FROM calendar_imports WHERE id = ? AND family_id = ?`,
		importID, familyID).Scan(&sealed, &etag, &createdBy, &parserVersion)
	if err != nil {
		return err
	}
	if !sealed.Valid {
		return errNotLinked
	}
	// Set before the fetch, so a failing source still counts as attempted and is not retried every pass.
	if _, err := h.db.ExecContext(ctx,
		`UPDATE calendar_imports SET last_attempt_at = ? WHERE id = ? AND family_id = ?`,
		dbTime(h.clock.Now()), importID, familyID); err != nil {
		return err
	}

	link, err := openLink(h.dbKey, importID, sealed.String)
	if err != nil {
		return h.recordSyncFailure(ctx, familyID, importID, "unavailable")
	}
	u, err := h.feeds.parseFeedURL(link)
	if err != nil {
		return h.recordSyncFailure(ctx, familyID, importID, feedErrorCode(err))
	}
	// An older parser may have stored different events, so ask for the whole calendar, not a 304.
	conditionalTag := etag.String
	if parserVersion < icsParserVersion {
		conditionalTag = ""
	}
	fetched, err := h.feeds.fetch(ctx, u, conditionalTag)
	if err != nil {
		h.logger.Info("calendar link refresh failed", "import", importID, "reason", feedErrorCode(err))
		return h.recordSyncFailure(ctx, familyID, importID, feedErrorCode(err))
	}
	if fetched.NotModified {
		_, err := h.db.ExecContext(ctx,
			`UPDATE calendar_imports SET last_synced_at = ?, last_sync_error = NULL WHERE id = ? AND family_id = ?`,
			dbTime(h.clock.Now()), importID, familyID)
		return err
	}

	zone, _, err := h.familyZone(ctx, familyID)
	if err != nil {
		return err
	}
	parsed, err := parseICS(fetched.Body, zone)
	if err != nil {
		return h.recordSyncFailure(ctx, familyID, importID, feedErrorCode(err))
	}
	if len(parsed.Series) == 0 {
		return h.recordSyncFailure(ctx, familyID, importID, "no_events")
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	memberIDs, err := importMemberIDs(ctx, tx, familyID, importID)
	if err != nil {
		return err
	}
	// Deleting the calendar's events also removes their changed occurrences and members. Events the
	// user added to a linked calendar go too, since linked calendars take no new events.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_events WHERE import_id = ? AND family_id = ?`, importID, familyID); err != nil {
		return err
	}
	now := h.clock.Now()
	if err := insertImportEvents(ctx, tx, familyID, createdBy, importID, memberIDs, parsed.Series, now); err != nil {
		return err
	}
	var newTag any
	if fetched.ETag != "" {
		newTag = fetched.ETag
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE calendar_imports
		 SET skipped_count = ?, duplicate_count = ?, source_etag = ?, parser_version = ?, last_synced_at = ?, last_sync_error = NULL
		 WHERE id = ? AND family_id = ?`,
		parsed.Skipped, parsed.Duplicates, newTag, icsParserVersion, dbTime(now), importID, familyID); err != nil {
		return err
	}
	return tx.Commit()
}

// claimSyncNow marks a manual sync as the calendar's attempt, unless it was attempted within
// syncNowCooldown. The check and the mark are one UPDATE, so two requests at once cannot both fetch.
func (h *Handlers) claimSyncNow(ctx context.Context, familyID, importID string) error {
	now := h.clock.Now()
	res, err := h.db.ExecContext(ctx,
		`UPDATE calendar_imports SET last_attempt_at = ?
		 WHERE id = ? AND family_id = ? AND source_url_sealed IS NOT NULL
		   AND (last_attempt_at IS NULL OR last_attempt_at <= ?)`,
		dbTime(now), importID, familyID, dbTime(now.Add(-syncNowCooldown)))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	var linked int
	err = h.db.QueryRowContext(ctx,
		`SELECT source_url_sealed IS NOT NULL FROM calendar_imports WHERE id = ? AND family_id = ?`,
		importID, familyID).Scan(&linked)
	if err != nil {
		return err
	}
	if linked == 0 {
		return errNotLinked
	}
	return errSyncTooSoon
}

// auditSync records a manual sync and the error it ended with, if any.
func (h *Handlers) auditSync(ctx context.Context, familyID, userID, importID string, syncErr *string) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := audit.Record(ctx, tx, familyID, userID, "calendar_import.synced", "calendar_import", &importID,
		map[string]any{"error": syncErr}); err != nil {
		return err
	}
	return tx.Commit()
}

// recordSyncFailure stores why a refresh failed. The events stay as they were.
func (h *Handlers) recordSyncFailure(ctx context.Context, familyID, importID, code string) error {
	if _, err := h.db.ExecContext(ctx,
		`UPDATE calendar_imports SET last_sync_error = ? WHERE id = ? AND family_id = ?`,
		code, importID, familyID); err != nil {
		return err
	}
	return diagnostics.Record(ctx, h.db, &familyID, diagnostics.KindCalendarSync, code, nil, "", h.clock.Now())
}

// dueLink is a linked calendar whose refresh interval has passed.
type dueLink struct {
	id, familyID string
	attempted    time.Time // zero when it has never been attempted
}

// SyncDueLinks refreshes the linked calendars whose refresh interval has passed, least recently
// attempted first, up to maxSyncPerPass. A calendar that fails is logged and the others still run.
// It returns how many it tried.
func (h *Handlers) SyncDueLinks(ctx context.Context) (int, error) {
	now := h.clock.Now()
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, family_id, refresh_minutes, last_attempt_at FROM calendar_imports
		 WHERE source_url_sealed IS NOT NULL AND paused = 0`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var due []dueLink
	for rows.Next() {
		var d dueLink
		var minutes int
		var attempted *storedTime
		if err := rows.Scan(&d.id, &d.familyID, &minutes, &attempted); err != nil {
			return 0, err
		}
		if attempted != nil {
			d.attempted = attempted.T
		}
		if !d.attempted.IsZero() && now.Sub(d.attempted) < time.Duration(minutes)*time.Minute {
			continue
		}
		due = append(due, d)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	sort.SliceStable(due, func(i, j int) bool { return due[i].attempted.Before(due[j].attempted) })
	if len(due) > maxSyncPerPass {
		due = due[:maxSyncPerPass]
	}
	for _, d := range due {
		if err := h.syncImportFeed(ctx, d.familyID, d.id); err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			h.logger.Error("calendar link sync failed", "import", d.id, "error", err)
		}
	}
	return len(due), nil
}

// SyncImport refreshes one linked calendar now and returns the import as it stands afterwards. A
// failed refresh is not an HTTP error: the response is 200 with last_sync_error set, and the events
// it had are kept.
func (h *Handlers) SyncImport(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !isUUID(id) {
		notFound(w, "Import not found")
		return
	}

	ctx := r.Context()
	err := h.claimSyncNow(ctx, familyID, id)
	if err == nil {
		err = h.syncImportFeed(ctx, familyID, id)
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		notFound(w, "Import not found")
		return
	case errors.Is(err, errNotLinked):
		web.RespondError(w, http.StatusBadRequest, "not_linked", "Only a linked calendar can be synced", nil)
		return
	case errors.Is(err, errSyncTooSoon):
		web.RespondError(w, http.StatusTooManyRequests, "sync_too_soon", "This calendar was synced a moment ago. Try again shortly.", nil)
		return
	case err != nil:
		h.internalError(w, "sync import", err)
		return
	}

	item, err := h.loadImport(ctx, familyID, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Import not found")
		return
	}
	if err != nil {
		h.internalError(w, "load import", err)
		return
	}
	if err := h.auditSync(ctx, familyID, userID, id, item.LastSyncError); err != nil {
		h.internalError(w, "audit sync import", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, item)
}

// loadImport reads one import with the people it is for.
func (h *Handlers) loadImport(ctx context.Context, familyID, id string) (importResponse, error) {
	item, err := scanImport(h.db.QueryRowContext(ctx, importColumns+` WHERE i.id = ? AND i.family_id = ?`, id, familyID))
	if err != nil {
		return importResponse{}, err
	}
	item.MemberIDs, err = importMemberIDs(ctx, h.db, familyID, id)
	if err != nil {
		return importResponse{}, err
	}
	return item, nil
}

// importMemberIDs lists the people an import is for, as stored.
func importMemberIDs(ctx context.Context, q querier, familyID, importID string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT family_member_id FROM calendar_import_members WHERE import_id = ? AND family_id = ? ORDER BY family_member_id`,
		importID, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
