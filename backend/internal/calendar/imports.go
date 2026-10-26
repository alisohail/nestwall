package calendar

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

const (
	// maxImportBytes is the largest calendar file accepted.
	maxImportBytes  = 10 << 20
	maxImportName   = 100
	maxImportFile   = 255
	defaultImported = "Imported calendar"
	defaultLinked   = "Linked calendar"
	defaultColour   = "#0D9488"
	defaultRefresh  = 30
)

var colourPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// refreshChoices are the minutes between syncs of a linked calendar.
var refreshChoices = map[int]bool{15: true, 30: true, 60: true, 360: true, 1440: true}

// importResponse is one import as the API lists it. Audience is 'everyone', 'specific' or 'parents';
// MemberIDs lists the people chosen for 'specific' and the parents it resolved to for 'parents'.
// Kind is 'file' for an uploaded .ics and 'link' for a calendar refreshed from a web address. The
// address itself is never returned.
type importResponse struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	FileName       *string  `json:"file_name"`
	EventCount     int      `json:"event_count"`
	SkippedCount   int      `json:"skipped_count"`
	DuplicateCount int      `json:"duplicate_count"`
	Colour         string   `json:"colour"`
	Audience       string   `json:"audience"`
	ShowOnWall     bool     `json:"show_on_wall"`
	MemberIDs      []string `json:"member_ids"`
	RefreshMinutes int      `json:"refresh_minutes"`
	Paused         bool     `json:"paused"`
	LastSyncedAt   *string  `json:"last_synced_at"`
	LastSyncError  *string  `json:"last_sync_error"`
	CreatedAt      string   `json:"created_at"`
}

// CreateImport reads an ICS file and stores its events as one import. Each event is a normal event
// in the family's local calendar, so it can be edited, and the import can be removed later as a unit.
func (h *Handlers) CreateImport(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+64<<10)
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			web.RespondError(w, http.StatusRequestEntityTooLarge, "file_too_large", "Calendar file must be 10 MB or smaller", nil)
			return
		}
		web.RespondError(w, http.StatusBadRequest, "invalid_form", "Upload must be multipart form data", nil)
		return
	}
	ctx := r.Context()
	link := strings.TrimSpace(r.FormValue("url"))
	file, header, fileErr := r.FormFile("file")
	if fileErr == nil {
		defer file.Close()
	}
	switch {
	case fileErr == nil && link != "":
		web.RespondError(w, http.StatusBadRequest, "one_source", "Upload a file or paste a link, not both", nil)
		return
	case fileErr != nil && link == "":
		web.RespondError(w, http.StatusBadRequest, "missing_file", "Choose an .ics file or paste a calendar link", nil)
		return
	}

	var (
		data     []byte
		fileName string
		etag     string
		err      error
	)
	if fileErr == nil {
		fileName = clip(baseName(header.Filename), maxImportFile)
		data, err = io.ReadAll(io.LimitReader(file, maxImportBytes+1))
		if err != nil {
			web.RespondError(w, http.StatusBadRequest, "invalid_form", "Could not read the calendar file", nil)
			return
		}
		if len(data) > maxImportBytes {
			web.RespondError(w, http.StatusRequestEntityTooLarge, "file_too_large", "Calendar file must be 10 MB or smaller", nil)
			return
		}
	} else {
		u, err := h.feeds.parseFeedURL(link)
		if err != nil {
			respondFeedError(w, err)
			return
		}
		fetched, err := h.feeds.fetch(ctx, u, "")
		if err != nil {
			respondFeedError(w, err)
			return
		}
		data, etag = fetched.Body, fetched.ETag
	}

	fallbackName := defaultImported
	if link != "" {
		fallbackName = defaultLinked
	}
	name, err := requiredText("name", importName(r.FormValue("name"), fileName, fallbackName), maxImportName)
	if err != nil {
		h.rejectInvalid(w, "import name", err)
		return
	}
	colour, err := importColour(r.FormValue("colour"))
	if err != nil {
		h.rejectInvalid(w, "import colour", err)
		return
	}
	showOnWall, err := importShowOnWall(r.FormValue("show_on_wall"))
	if err != nil {
		h.rejectInvalid(w, "import show_on_wall", err)
		return
	}
	refresh, err := importRefresh(r.FormValue("refresh_minutes"))
	if err != nil {
		h.rejectInvalid(w, "import refresh_minutes", err)
		return
	}

	zone, _, err := h.familyZone(ctx, familyID)
	if err != nil {
		h.internalError(w, "load family zone", err)
		return
	}
	parsed, err := parseICS(data, zone)
	switch {
	case errors.Is(err, errInvalidICS):
		web.RespondError(w, http.StatusBadRequest, "invalid_ics", "The file is not a valid calendar (.ics) file", nil)
		return
	case errors.Is(err, errTooManyEvents):
		web.RespondError(w, http.StatusBadRequest, "too_many_events", "A calendar file can have at most 5000 events", nil)
		return
	case err != nil:
		h.internalError(w, "parse ics", err)
		return
	}
	if len(parsed.Series) == 0 {
		web.RespondError(w, http.StatusBadRequest, "no_events", "No events in this file could be imported",
			map[string]any{"skipped_count": parsed.Skipped})
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin import", err)
		return
	}
	defer tx.Rollback()

	audience, memberIDs, err := importAudience(ctx, tx, familyID, r.FormValue("audience"), r.MultipartForm.Value["member_ids"])
	if err != nil {
		h.rejectInvalid(w, "import audience", err)
		return
	}

	now := h.clock.Now()
	importID := ids.New()
	stamp := dbTime(now)
	var fileNameValue *string
	if fileName != "" {
		fileNameValue = &fileName
	}
	var sealed, etagValue, syncedValue *string
	kind := "file"
	if link != "" {
		kind = "link"
		s, err := sealLink(h.dbKey, importID, link)
		if err != nil {
			h.internalError(w, "seal calendar link", err)
			return
		}
		sealed, syncedValue = &s, &stamp
		if etag != "" {
			etagValue = &etag
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO calendar_imports (id, family_id, name, file_name, skipped_count, duplicate_count, colour, audience, show_on_wall,
		                              source_url_sealed, source_etag, last_synced_at, last_attempt_at, refresh_minutes,
		                              parser_version, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		importID, familyID, name, fileNameValue, parsed.Skipped, parsed.Duplicates, colour, audience, boolInt(showOnWall),
		sealed, etagValue, syncedValue, syncedValue, refresh, icsParserVersion, userID, stamp, stamp); err != nil {
		h.internalError(w, "insert import", err)
		return
	}
	if err := insertImportMembers(ctx, tx, familyID, importID, memberIDs, now); err != nil {
		h.internalError(w, "insert import members", err)
		return
	}
	if err := insertImportEvents(ctx, tx, familyID, userID, importID, memberIDs, parsed.Series, now); err != nil {
		h.internalError(w, "insert import events", err)
		return
	}

	if err := audit.Record(ctx, tx, familyID, userID, "calendar_import.created", "calendar_import", &importID,
		map[string]any{"kind": kind, "events": len(parsed.Series), "skipped": parsed.Skipped, "duplicates": parsed.Duplicates, "audience": audience}); err != nil {
		h.internalError(w, "audit import", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit import", err)
		return
	}
	created := now.UTC().Format(time.RFC3339)
	response := importResponse{
		ID:             importID,
		Name:           name,
		Kind:           kind,
		FileName:       fileNameValue,
		EventCount:     len(parsed.Series),
		SkippedCount:   parsed.Skipped,
		DuplicateCount: parsed.Duplicates,
		Colour:         colour,
		Audience:       audience,
		ShowOnWall:     showOnWall,
		MemberIDs:      memberIDs,
		RefreshMinutes: refresh,
		CreatedAt:      created,
	}
	if link != "" {
		response.LastSyncedAt = &created
	}
	web.RespondJSON(w, http.StatusCreated, response)
}

// insertImportEvents stores the parsed events of an import in the family's local calendar. Each
// event is tagged with the import, gets the import's audience, and keeps its cancelled and changed
// dates.
func insertImportEvents(ctx context.Context, tx *sql.Tx, familyID, userID, importID string, memberIDs []string,
	series []importedSeries, now time.Time) error {
	sourceID, err := ensureLocalSource(ctx, tx, familyID, now)
	if err != nil {
		return err
	}
	target := calendarTarget{sourceID: sourceID, importID: &importID}
	for _, s := range series {
		eventID := ids.New()
		if err := insertEvent(ctx, tx, familyID, userID, eventID, target, true, s.Event, now); err != nil {
			return err
		}
		if len(memberIDs) > 0 {
			if err := replaceMembers(ctx, tx, familyID, eventID, memberIDs, now); err != nil {
				return err
			}
		}
		if err := replaceReminders(ctx, tx, familyID, eventID, s.Event.reminders, s.Event.timing.startsAt, now, s.Event.rule != nil, true); err != nil {
			return err
		}
		// Cancellations go first, so an override for the same date replaces them.
		for _, date := range s.Cancelled {
			if err := upsertException(ctx, tx, familyID, eventID, date, nil, now); err != nil {
				return err
			}
		}
		for _, e := range s.Edited {
			occurrence := e.Occurrence
			if err := upsertException(ctx, tx, familyID, eventID, e.Date, &occurrence, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// importColumns selects an import the way scanImport reads it. The link itself is never selected.
const importColumns = `SELECT i.id, i.name, i.file_name, i.skipped_count, i.duplicate_count, i.colour, i.audience, i.show_on_wall, i.created_at,
		        i.source_url_sealed IS NOT NULL, i.last_synced_at, i.last_sync_error, i.refresh_minutes, i.paused,
		        (SELECT COUNT(*) FROM calendar_events e WHERE e.import_id = i.id AND e.family_id = i.family_id)
		 FROM calendar_imports i`

// scanImport reads one row selected with importColumns. The member IDs are filled in by the caller.
func scanImport(s rowScanner) (importResponse, error) {
	var item importResponse
	var fileName, syncError sql.NullString
	var created storedTime
	var synced *storedTime
	var isLink, paused int
	if err := s.Scan(&item.ID, &item.Name, &fileName, &item.SkippedCount, &item.DuplicateCount, &item.Colour, &item.Audience,
		&item.ShowOnWall, &created, &isLink, &synced, &syncError, &item.RefreshMinutes, &paused,
		&item.EventCount); err != nil {
		return importResponse{}, err
	}
	if fileName.Valid {
		item.FileName = &fileName.String
	}
	item.Kind = "file"
	if isLink == 1 {
		item.Kind = "link"
	}
	if synced != nil {
		s := synced.T.Format(time.RFC3339)
		item.LastSyncedAt = &s
	}
	if syncError.Valid {
		item.LastSyncError = &syncError.String
	}
	item.Paused = paused == 1
	item.CreatedAt = created.T.Format(time.RFC3339)
	item.MemberIDs = []string{}
	return item, nil
}

// ListImports returns the family's imports, oldest first, with the number of events each still has.
func (h *Handlers) ListImports(w http.ResponseWriter, r *http.Request) {
	familyID, _, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	rows, err := h.db.QueryContext(ctx, importColumns+` WHERE i.family_id = ? ORDER BY i.created_at, i.id`, familyID)
	if err != nil {
		h.internalError(w, "list imports", err)
		return
	}
	defer rows.Close()

	items := []importResponse{}
	index := map[string]int{}
	for rows.Next() {
		item, err := scanImport(rows)
		if err != nil {
			h.internalError(w, "scan import", err)
			return
		}
		index[item.ID] = len(items)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "read imports", err)
		return
	}

	members, err := h.db.QueryContext(ctx,
		`SELECT import_id, family_member_id FROM calendar_import_members WHERE family_id = ? ORDER BY family_member_id`,
		familyID)
	if err != nil {
		h.internalError(w, "list import members", err)
		return
	}
	defer members.Close()
	for members.Next() {
		var importID, memberID string
		if err := members.Scan(&importID, &memberID); err != nil {
			h.internalError(w, "scan import member", err)
			return
		}
		if i, ok := index[importID]; ok {
			items[i].MemberIDs = append(items[i].MemberIDs, memberID)
		}
	}
	if err := members.Err(); err != nil {
		h.internalError(w, "read import members", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, map[string]any{"items": items})
}

// DeleteImport removes an import and every event it brought in. The events' changed occurrences
// and members go with them. Another family's import is reported as not found.
func (h *Handlers) DeleteImport(w http.ResponseWriter, r *http.Request) {
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
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin delete import", err)
		return
	}
	defer tx.Rollback()

	var events int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_events WHERE import_id = ? AND family_id = ?`,
		id, familyID).Scan(&events); err != nil {
		h.internalError(w, "count import events", err)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE calendar_events SET import_id = NULL WHERE import_id = ? AND family_id = ? AND from_feed = 0`,
		id, familyID); err != nil {
		h.internalError(w, "keep user events", err)
		return
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM calendar_imports WHERE id = ? AND family_id = ?`, id, familyID)
	if err != nil {
		h.internalError(w, "delete import", err)
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		h.internalError(w, "delete import rows", err)
		return
	}
	if n == 0 {
		notFound(w, "Import not found")
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "calendar_import.deleted", "calendar_import", &id,
		map[string]any{"events": events}); err != nil {
		h.internalError(w, "audit delete import", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit delete import", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateImportRequest struct {
	Name           string   `json:"name"`
	Colour         string   `json:"colour"`
	Audience       *string  `json:"audience"`
	MemberIDs      []string `json:"member_ids"`
	ShowOnWall     *bool    `json:"show_on_wall"`
	RefreshMinutes *int     `json:"refresh_minutes"`
	Paused         *bool    `json:"paused"`
}

// UpdateImport changes an import's name, colour and wall switch. A given audience (with member_ids for
// 'specific') replaces who it is for, on the import and on each of its events. For a linked calendar it
// also sets the refresh interval and whether it is paused. Events read these from the import or from
// their own member rows, so the new values show on them straight away. Another family's import is
// reported as not found.
func (h *Handlers) UpdateImport(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !isUUID(id) {
		notFound(w, "Import not found")
		return
	}
	var req updateImportRequest
	if !decodeBody(w, r, &req) {
		return
	}
	name, err := requiredText("name", req.Name, maxImportName)
	if err != nil {
		h.rejectInvalid(w, "import name", err)
		return
	}
	colour, err := importColour(req.Colour)
	if err != nil {
		h.rejectInvalid(w, "import colour", err)
		return
	}
	// A nil argument keeps the stored value, through COALESCE in the UPDATE.
	var audience, showOnWall, refresh, paused any
	details := map[string]any{"name": name, "colour": colour}
	if req.ShowOnWall != nil {
		showOnWall = boolInt(*req.ShowOnWall)
		details["show_on_wall"] = *req.ShowOnWall
	}
	if req.RefreshMinutes != nil {
		if !refreshChoices[*req.RefreshMinutes] {
			h.rejectInvalid(w, "import refresh_minutes", fieldError("refresh_minutes", "invalid"))
			return
		}
		refresh = *req.RefreshMinutes
		details["refresh_minutes"] = *req.RefreshMinutes
	}
	if req.Paused != nil {
		paused = boolInt(*req.Paused)
		details["paused"] = *req.Paused
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin update import", err)
		return
	}
	defer tx.Rollback()

	var members []string
	if req.Audience != nil {
		value, memberIDs, err := importAudience(ctx, tx, familyID, *req.Audience, req.MemberIDs)
		if err != nil {
			h.rejectInvalid(w, "import audience", err)
			return
		}
		audience, members = value, memberIDs
		details["audience"] = value
		details["member_count"] = len(memberIDs)
	}

	now := h.clock.Now()
	res, err := tx.ExecContext(ctx,
		`UPDATE calendar_imports
		 SET name = ?, colour = ?, audience = COALESCE(?, audience), show_on_wall = COALESCE(?, show_on_wall),
		     refresh_minutes = COALESCE(?, refresh_minutes), paused = COALESCE(?, paused), updated_at = ?
		 WHERE id = ? AND family_id = ?`,
		name, colour, audience, showOnWall, refresh, paused, dbTime(now), id, familyID)
	if err != nil {
		h.internalError(w, "update import", err)
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		h.internalError(w, "update import rows", err)
		return
	}
	if n == 0 {
		notFound(w, "Import not found")
		return
	}
	if req.Audience != nil {
		if err := setImportAudience(ctx, tx, familyID, id, members, now); err != nil {
			h.internalError(w, "set import audience", err)
			return
		}
	}
	if err := audit.Record(ctx, tx, familyID, userID, "calendar_import.updated", "calendar_import", &id, details); err != nil {
		h.internalError(w, "audit update import", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit update import", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// importColour checks an import's colour, a #RRGGBB value. An empty colour gets the default.
func importColour(raw string) (string, error) {
	c := strings.TrimSpace(raw)
	if c == "" {
		return defaultColour, nil
	}
	if !colourPattern.MatchString(c) {
		return "", fieldError("colour", "invalid")
	}
	return strings.ToUpper(c), nil
}

// importShowOnWall reads the wall switch. An absent value means on, the default for a new import.
func importShowOnWall(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "true", "1", "on":
		return true, nil
	case "false", "0", "off":
		return false, nil
	}
	return false, fieldError("show_on_wall", "invalid")
}

// importRefresh reads the refresh interval for a new import. An absent value keeps the default.
func importRefresh(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultRefresh, nil
	}
	minutes, err := strconv.Atoi(trimmed)
	if err != nil || !refreshChoices[minutes] {
		return 0, fieldError("refresh_minutes", "invalid")
	}
	return minutes, nil
}

// importAudience works out who an import is for. 'everyone' has no people. 'specific' needs at
// least one active member. 'parents' is the family's active parents at the time of the import.
func importAudience(ctx context.Context, q querier, familyID, audience string, raw []string) (string, []string, error) {
	switch audience {
	case "", "everyone":
		return "everyone", []string{}, nil
	case "specific":
		ids, err := normaliseMembers(raw)
		if err != nil {
			return "", nil, err
		}
		if len(ids) == 0 {
			return "", nil, fieldError("member_ids", "required")
		}
		if err := validateMembers(ctx, q, familyID, ids); err != nil {
			return "", nil, err
		}
		return "specific", ids, nil
	case "parents":
		ids, err := activeParents(ctx, q, familyID)
		if err != nil {
			return "", nil, err
		}
		return "parents", ids, nil
	}
	return "", nil, fieldError("audience", "invalid")
}

// activeParents lists the family's active members with the Parent role.
func activeParents(ctx context.Context, q querier, familyID string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT m.id FROM family_members m
		 JOIN roles r ON r.id = m.role_id
		 WHERE m.family_id = ? AND m.status = 'active' AND r.name = 'Parent'
		 ORDER BY m.id`, familyID)
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

func insertImportMembers(ctx context.Context, tx *sql.Tx, familyID, importID string, memberIDs []string, now time.Time) error {
	stamp := dbTime(now)
	for _, memberID := range memberIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO calendar_import_members (id, family_id, import_id, family_member_id, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			ids.New(), familyID, importID, memberID, stamp); err != nil {
			return err
		}
	}
	return nil
}

// setImportAudience replaces who an import is for. Later syncs read the import's members, and the
// events read their own member rows, so both change together. An empty list makes the events
// family-wide.
func setImportAudience(ctx context.Context, tx *sql.Tx, familyID, importID string, memberIDs []string, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_import_members WHERE family_id = ? AND import_id = ?`, familyID, importID); err != nil {
		return err
	}
	if err := insertImportMembers(ctx, tx, familyID, importID, memberIDs, now); err != nil {
		return err
	}
	// The rows are read in full before any event is written, so the transaction has one open query at a time.
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM calendar_events WHERE family_id = ? AND import_id = ?`, familyID, importID)
	if err != nil {
		return err
	}
	var eventIDs []string
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			rows.Close()
			return err
		}
		eventIDs = append(eventIDs, eventID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, eventID := range eventIDs {
		if err := replaceMembers(ctx, tx, familyID, eventID, memberIDs, now); err != nil {
			return err
		}
	}
	return nil
}

// importName uses the name the user typed, or the file name without its extension, or the fallback
// for a linked calendar.
func importName(typed, fileName, fallback string) string {
	if t := strings.TrimSpace(typed); t != "" {
		return t
	}
	if i := strings.LastIndex(fileName, "."); i > 0 {
		fileName = fileName[:i]
	}
	if t := strings.TrimSpace(fileName); t != "" {
		return clip(t, maxImportName)
	}
	return fallback
}

// baseName returns the last part of an uploaded path, which browsers may send with directories.
func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}
