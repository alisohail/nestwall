package calendar

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
	"nestwall/backend/internal/tasks"
)

// maxJSONBody bounds every request body in this package.
const maxJSONBody = 64 << 10

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Handlers serves the calendar endpoints. Writes are versioned with If-Match and audited, as in
// the tasks package.
type Handlers struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
	dbKey  string
	feeds  *feedFetcher
}

// NewHandlers creates the calendar handlers. dbKey seals the links of external calendars.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger, dbKey string) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger, dbKey: dbKey, feeds: newFeedFetcher(false, feedTimeout)}
}

// eventResponse is an event as the API returns it. The end is the last day and time of the
// first occurrence, in the event's zone.
type eventResponse struct {
	ID             string              `json:"id"`
	Title          string              `json:"title"`
	Description    *string             `json:"description"`
	Location       *string             `json:"location"`
	AllDay         bool                `json:"all_day"`
	TimeZone       string              `json:"time_zone"`
	StartDate      string              `json:"start_date"`
	StartTime      *string             `json:"start_time"`
	EndDate        string              `json:"end_date"`
	EndTime        *string             `json:"end_time"`
	RecurrenceRule *string             `json:"recurrence_rule"`
	MemberIDs      []string            `json:"member_ids"`
	Version        int                 `json:"version"`
	Imported       bool                `json:"imported"`
	CalendarID     string              `json:"calendar_id"`
	Exceptions     []exceptionResponse `json:"exceptions"`
	Reminders      []int               `json:"reminders"`
}

// exceptionResponse is one changed occurrence. A cancelled occurrence has only its date and kind.
type exceptionResponse struct {
	OccurrenceDate string  `json:"occurrence_date"`
	Kind           string  `json:"kind"`
	Title          *string `json:"title,omitempty"`
	Description    *string `json:"description,omitempty"`
	Location       *string `json:"location,omitempty"`
	StartDate      *string `json:"start_date,omitempty"`
	StartTime      *string `json:"start_time,omitempty"`
	EndDate        *string `json:"end_date,omitempty"`
	EndTime        *string `json:"end_time,omitempty"`
}

// CreateEvent adds a local event for the family.
func (h *Handlers) CreateEvent(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	var req eventRequest
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := req.check()
	if err != nil {
		h.rejectInvalid(w, "check event", err)
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin create event", err)
		return
	}
	defer tx.Rollback()

	if err := validateMembers(ctx, tx, familyID, v.memberIDs); err != nil {
		h.rejectInvalid(w, "validate members", err)
		return
	}
	now := h.clock.Now()
	target, err := chooseCalendar(ctx, tx, familyID, req.CalendarID, calendarTarget{}, now)
	if err != nil {
		h.rejectInvalid(w, "choose calendar", err)
		return
	}
	memberIDs, err := eventMemberIDs(ctx, tx, familyID, target, v.memberIDs)
	if err != nil {
		h.internalError(w, "load calendar members", err)
		return
	}
	v.memberIDs = memberIDs
	id := ids.New()
	if err := insertEvent(ctx, tx, familyID, userID, id, target, false, v, now); err != nil {
		h.internalError(w, "insert event", err)
		return
	}
	if err := replaceMembers(ctx, tx, familyID, id, v.memberIDs, now); err != nil {
		h.internalError(w, "insert event members", err)
		return
	}
	if err := replaceReminders(ctx, tx, familyID, id, v.reminders, v.timing.startsAt, now, v.rule != nil, true); err != nil {
		h.internalError(w, "insert event reminders", err)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "calendar_event.created", "calendar_event", &id,
		map[string]any{"all_day": v.allDay, "repeats": v.rule != nil}); err != nil {
		h.internalError(w, "audit create event", err)
		return
	}

	resp, err := h.reload(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "reload event", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit create event", err)
		return
	}
	setETag(w, resp.Version)
	web.RespondJSON(w, http.StatusCreated, resp)
}

// GetEvent returns one event. A caller with calendar.view at own level gets events it is a member of.
func (h *Handlers) GetEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Event not found")
		return
	}

	ctx := r.Context()
	if !h.visible(ctx, w, familyID, id, scope) {
		return
	}
	resp, err := h.reload(ctx, h.db, familyID, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Event not found")
		return
	}
	if err != nil {
		h.internalError(w, "load event", err)
		return
	}
	setETag(w, resp.Version)
	web.RespondJSON(w, http.StatusOK, resp)
}

// UpdateEvent replaces an event. Its occurrence changes are kept unless the event stops repeating.
func (h *Handlers) UpdateEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Event not found")
		return
	}
	var req eventRequest
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := req.check()
	if err != nil {
		h.rejectInvalid(w, "check event", err)
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin update event", err)
		return
	}
	defer tx.Rollback()

	current, err := loadEvent(ctx, tx, familyID, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Event not found")
		return
	}
	if err != nil {
		h.internalError(w, "load event", err)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}
	if current.FromFeed {
		refuseImported(w)
		return
	}
	if err := validateMembers(ctx, tx, familyID, v.memberIDs); err != nil {
		h.rejectInvalid(w, "validate members", err)
		return
	}

	now := h.clock.Now()
	target, err := chooseCalendar(ctx, tx, familyID, req.CalendarID,
		calendarTarget{sourceID: current.SourceID, importID: current.ImportID}, now)
	if err != nil {
		h.rejectInvalid(w, "choose calendar", err)
		return
	}
	memberIDs, err := eventMemberIDs(ctx, tx, familyID, target, v.memberIDs)
	if err != nil {
		h.internalError(w, "load calendar members", err)
		return
	}
	v.memberIDs = memberIDs
	written, err := updateEvent(ctx, tx, familyID, id, current.Version, target, v, now)
	if err != nil {
		h.internalError(w, "update event", err)
		return
	}
	if !written {
		versionConflict(w, current.Version)
		return
	}
	if err := replaceMembers(ctx, tx, familyID, id, v.memberIDs, now); err != nil {
		h.internalError(w, "replace event members", err)
		return
	}
	// A moved event is a new start, so its reminders are armed again from now.
	if err := replaceReminders(ctx, tx, familyID, id, v.reminders, v.timing.startsAt, now, v.rule != nil,
		!current.StartsAt.Equal(v.timing.startsAt)); err != nil {
		h.internalError(w, "replace event reminders", err)
		return
	}
	if v.rule == nil && current.Rule != nil {
		if err := deleteExceptions(ctx, tx, familyID, id); err != nil {
			h.internalError(w, "clear occurrence changes", err)
			return
		}
	}
	if err := audit.Record(ctx, tx, familyID, userID, "calendar_event.updated", "calendar_event", &id,
		map[string]any{"all_day": v.allDay, "repeats": v.rule != nil}); err != nil {
		h.internalError(w, "audit update event", err)
		return
	}

	resp, err := h.reload(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "reload event", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit update event", err)
		return
	}
	setETag(w, resp.Version)
	web.RespondJSON(w, http.StatusOK, resp)
}

// DeleteEvent removes an event with its members and occurrence changes.
func (h *Handlers) DeleteEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Event not found")
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin delete event", err)
		return
	}
	defer tx.Rollback()

	current, err := loadEvent(ctx, tx, familyID, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Event not found")
		return
	}
	if err != nil {
		h.internalError(w, "load event", err)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}
	if current.FromFeed {
		refuseImported(w)
		return
	}
	deleted, err := deleteEvent(ctx, tx, familyID, id, current.Version)
	if err != nil {
		h.internalError(w, "delete event", err)
		return
	}
	if !deleted {
		versionConflict(w, current.Version)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "calendar_event.deleted", "calendar_event", &id, nil); err != nil {
		h.internalError(w, "audit delete event", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit delete event", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// EditOccurrence changes one occurrence of a repeating event. The other occurrences are unchanged.
func (h *Handlers) EditOccurrence(w http.ResponseWriter, r *http.Request) {
	h.changeOccurrence(w, r, true)
}

// CancelOccurrence removes one occurrence of a repeating event. The other occurrences are unchanged.
func (h *Handlers) CancelOccurrence(w http.ResponseWriter, r *http.Request) {
	h.changeOccurrence(w, r, false)
}

// changeOccurrence is the shared path for editing and cancelling one occurrence. The checks run
// in this order: the event exists, If-Match, the event repeats, the date is valid, and the date is
// an occurrence of the event.
func (h *Handlers) changeOccurrence(w http.ResponseWriter, r *http.Request, edit bool) {
	id := r.PathValue("id")
	date := r.PathValue("date")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Event not found")
		return
	}

	var req occurrenceRequest
	if edit && !decodeBody(w, r, &req) {
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin occurrence change", err)
		return
	}
	defer tx.Rollback()

	current, err := loadEvent(ctx, tx, familyID, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Event not found")
		return
	}
	if err != nil {
		h.internalError(w, "load event", err)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}
	if current.FromFeed {
		refuseImported(w)
		return
	}
	if current.Rule == nil {
		web.RespondError(w, http.StatusConflict, "not_recurring", "This event does not repeat", nil)
		return
	}
	if _, err := parseDate("occurrence_date", date); err != nil {
		h.rejectInvalid(w, "check occurrence date", err)
		return
	}
	exists, err := occurrenceExists(current, date)
	if err != nil {
		h.internalError(w, "check occurrence", err)
		return
	}
	if !exists {
		notFound(w, "Occurrence not found")
		return
	}

	var edited *validatedOccurrence
	if edit {
		zone, err := time.LoadLocation(current.TimeZone)
		if err != nil {
			h.internalError(w, "load event zone", err)
			return
		}
		v, err := req.check(current.AllDay, zone)
		if err != nil {
			h.rejectInvalid(w, "check occurrence", err)
			return
		}
		edited = &v
	}

	now := h.clock.Now()
	if err := upsertException(ctx, tx, familyID, id, date, edited, now); err != nil {
		h.internalError(w, "save occurrence change", err)
		return
	}
	written, err := bumpVersion(ctx, tx, familyID, id, current.Version, now)
	if err != nil {
		h.internalError(w, "bump event version", err)
		return
	}
	if !written {
		versionConflict(w, current.Version)
		return
	}
	action := "calendar_event.occurrence_cancelled"
	if edit {
		action = "calendar_event.occurrence_edited"
	}
	if err := audit.Record(ctx, tx, familyID, userID, action, "calendar_event", &id,
		map[string]any{"occurrence_date": date}); err != nil {
		h.internalError(w, "audit occurrence change", err)
		return
	}

	resp, err := h.reload(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "reload event", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit occurrence change", err)
		return
	}
	setETag(w, resp.Version)
	if !edit {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	web.RespondJSON(w, http.StatusOK, resp)
}

// readScope checks that the caller may read the calendar. It returns the family, and the member
// whose events the caller may see. An empty member means every event (calendar.view at full or
// view level). A caller with calendar.view at own level sees the events it is a member of, and events
// from an import for everyone. A caller without calendar.view gets 403.
func (h *Handlers) readScope(w http.ResponseWriter, r *http.Request) (familyID, scope string, ok bool) {
	familyID, _, ok = h.memberFamily(w, r)
	if !ok {
		return "", "", false
	}
	ctx := r.Context()
	memberID, _ := permissions.GetFamilyMemberID(ctx)

	policy := permissions.NewPolicy(h.db)
	all, err := policy.Check(ctx, memberID, permissions.PermCalendarView)
	if err != nil {
		h.internalError(w, "check calendar.view", err)
		return "", "", false
	}
	if !all.Allowed {
		web.RespondError(w, http.StatusForbidden, "forbidden", "You do not have permission for this action", nil)
		return "", "", false
	}
	if all.AccessLevel != permissions.AccessOwn {
		return familyID, "", true
	}
	return familyID, memberID, true
}

// visible reports whether the event may be shown to the scope. A scope of "" sees every event. An
// event the scope may not see is reported as not found, as is one from another family.
func (h *Handlers) visible(ctx context.Context, w http.ResponseWriter, familyID, eventID, scope string) bool {
	if scope != "" {
		member, err := isMember(ctx, h.db, familyID, eventID, scope)
		if err != nil {
			h.internalError(w, "check event visibility", err)
			return false
		}
		if !member {
			notFound(w, "Event not found")
			return false
		}
	}
	return true
}

// reload reads an event and turns it into the response form.
func (h *Handlers) reload(ctx context.Context, q querier, familyID, id string) (eventResponse, error) {
	e, err := loadEvent(ctx, q, familyID, id)
	if err != nil {
		return eventResponse{}, err
	}
	return toEventResponse(e)
}

// occurrenceExists reports whether date is one of the occurrences of a repeating event, before
// any change to it.
func occurrenceExists(e event, date string) (bool, error) {
	startDay, err := time.Parse(dateLayout, e.StartDate)
	if err != nil {
		return false, err
	}
	day, err := time.Parse(dateLayout, date)
	if err != nil {
		return false, err
	}
	dates, err := tasks.OccurrenceDates(*e.Rule, startDay, day, day)
	if err != nil {
		return false, err
	}
	return len(dates) > 0, nil
}

// toEventResponse converts a stored event into the response form, in the event's zone.
func toEventResponse(e event) (eventResponse, error) {
	zone, err := time.LoadLocation(e.TimeZone)
	if err != nil {
		return eventResponse{}, err
	}
	startDay, err := time.Parse(dateLayout, e.StartDate)
	if err != nil {
		return eventResponse{}, err
	}

	resp := eventResponse{
		ID:             e.ID,
		Title:          e.Title,
		Description:    e.Description,
		Location:       e.Location,
		AllDay:         e.AllDay,
		TimeZone:       e.TimeZone,
		StartDate:      e.StartDate,
		StartTime:      e.StartTime,
		RecurrenceRule: e.Rule,
		MemberIDs:      e.MemberIDs,
		Version:        e.Version,
		Imported:       e.FromFeed,
		Reminders:      e.Reminders,
		Exceptions:     make([]exceptionResponse, 0, len(e.Exceptions)),
	}
	if e.ImportID != nil {
		resp.CalendarID = *e.ImportID
	} else {
		resp.CalendarID = e.SourceID
	}
	if e.AllDay {
		resp.EndDate = startDay.AddDate(0, 0, *e.SpanDays).Format(dateLayout)
	} else {
		endDate, endClock := localParts(e.EndsAt, zone, false)
		resp.EndDate, resp.EndTime = endDate, endClock
	}

	for _, x := range e.Exceptions {
		resp.Exceptions = append(resp.Exceptions, exceptionToResponse(x, zone, e.AllDay))
	}
	return resp, nil
}

// exceptionToResponse converts one stored occurrence change. An all-day end is the last day
// covered, which is the day before the stored end instant.
func exceptionToResponse(x exception, zone *time.Location, allDay bool) exceptionResponse {
	out := exceptionResponse{OccurrenceDate: x.OccurrenceDate, Kind: x.Kind}
	if x.Kind == "cancelled" {
		return out
	}
	out.Title = x.Title
	out.Description = x.Description
	out.Location = x.Location
	startDate, startClock := localParts(x.StartsAt, zone, allDay)
	out.StartDate, out.StartTime = &startDate, startClock
	if allDay {
		end := x.EndsAt.In(zone).AddDate(0, 0, -1).Format(dateLayout)
		out.EndDate = &end
		return out
	}
	endDate, endClock := localParts(x.EndsAt, zone, false)
	out.EndDate, out.EndTime = &endDate, endClock
	return out
}

// localParts returns the local date of an instant and, for a timed item, its local HH:MM.
func localParts(t time.Time, zone *time.Location, allDay bool) (string, *string) {
	local := t.In(zone)
	date := local.Format(dateLayout)
	if allDay {
		return date, nil
	}
	clock := local.Format("15:04")
	return date, &clock
}

// memberFamily returns the family of the signed-in member and the user ID. It writes the
// error response and returns ok=false when the request may not continue.
func (h *Handlers) memberFamily(w http.ResponseWriter, r *http.Request) (familyID, userID string, ok bool) {
	ctx := r.Context()
	memberID, okMember := permissions.GetFamilyMemberID(ctx)
	userID, okUser := permissions.GetUserID(ctx)
	if !okMember || !okUser {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return "", "", false
	}

	err := h.db.QueryRowContext(ctx,
		`SELECT family_id FROM family_members WHERE id = ? AND status = 'active'`, memberID,
	).Scan(&familyID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No active family membership", nil)
		return "", "", false
	}
	if err != nil {
		h.internalError(w, "load membership", err)
		return "", "", false
	}
	return familyID, userID, true
}

// decodeBody decodes a bounded JSON body and rejects unknown fields. It writes a 400 response
// and returns false on failure.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return false
	}
	return true
}

// rejectInvalid writes a 400 for a field the caller got wrong, and a 500 for anything else.
// refuseImported answers a change to a synced event. Synced events follow their source calendar, so
// they cannot be edited or deleted here; the next sync would replace the change anyway.
func refuseImported(w http.ResponseWriter) {
	web.RespondError(w, http.StatusConflict, "imported_event",
		"This event comes from a synced calendar and cannot be changed here", nil)
}

func (h *Handlers) rejectInvalid(w http.ResponseWriter, what string, err error) {
	var fieldErr *FieldError
	if errors.As(err, &fieldErr) {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input",
			map[string]any{"field": fieldErr.Field, "reason": fieldErr.Reason})
		return
	}
	h.internalError(w, what, err)
}

// internalError logs the cause and writes a generic 500 response.
func (h *Handlers) internalError(w http.ResponseWriter, what string, err error) {
	h.logger.Error("calendar: "+what, "error", err)
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}

func notFound(w http.ResponseWriter, message string) {
	web.RespondError(w, http.StatusNotFound, "not_found", message, nil)
}

func versionConflict(w http.ResponseWriter, current int) {
	web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
		"This changed since you loaded it. Reload and try again.", map[string]any{"version": current})
}

// isUUID reports whether s is a canonical UUID string.
func isUUID(s string) bool {
	return uuidPattern.MatchString(s)
}
