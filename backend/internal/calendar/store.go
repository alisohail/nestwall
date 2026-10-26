package calendar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"nestwall/backend/internal/platform/ids"
)

const (
	dbTimeLayout     = "2006-01-02 15:04:05.000"
	storedTimeLayout = dbTimeLayout + "Z07:00"
)

// querier is satisfied by both *sql.DB and *sql.Tx, so read helpers work in either.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type rowScanner interface {
	Scan(dest ...any) error
}

// dbTime formats a UTC instant the way the schema stores timestamps, so that text order is time
// order.
func dbTime(t time.Time) string {
	return t.UTC().Format(dbTimeLayout) + "+00:00"
}

// storedTime reads a DATETIME column. The driver may hand back a time.Time or the stored text,
// so both are accepted.
type storedTime struct {
	T time.Time
}

func (s *storedTime) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		s.T = v.UTC()
		return nil
	case string:
		return s.parse(v)
	case []byte:
		return s.parse(string(v))
	}
	return fmt.Errorf("calendar: unexpected time value of type %T", src)
}

func (s *storedTime) parse(text string) error {
	t, err := time.Parse(storedTimeLayout, text)
	if err != nil {
		return err
	}
	s.T = t.UTC()
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// event is a stored event with its members and its changed occurrences.
type event struct {
	ID              string
	SourceID        string
	Title           string
	Description     *string
	Location        *string
	AllDay          bool
	TimeZone        string
	StartDate       string
	StartTime       *string
	DurationMinutes *int
	SpanDays        *int
	StartsAt        time.Time
	EndsAt          time.Time
	Rule            *string
	Version         int
	CreatedBy       string
	ImportID        *string
	FromFeed        bool
	MemberIDs       []string
	Exceptions      []exception
	Reminders       []int
}

// exception is a stored change to one occurrence of a repeating event.
type exception struct {
	OccurrenceDate string
	Kind           string
	Title          *string
	Description    *string
	Location       *string
	StartsAt       time.Time
	EndsAt         time.Time
}

const eventColumns = `id, source_id, title, description, location, all_day, time_zone,
	start_date, start_time, duration_minutes, span_days, starts_at, ends_at,
	recurrence_rule, version, created_by, import_id, from_feed`

func scanEvent(s rowScanner, e *event) error {
	var allDay, fromFeed int
	var starts, ends storedTime
	err := s.Scan(&e.ID, &e.SourceID, &e.Title, &e.Description, &e.Location, &allDay, &e.TimeZone,
		&e.StartDate, &e.StartTime, &e.DurationMinutes, &e.SpanDays, &starts, &ends,
		&e.Rule, &e.Version, &e.CreatedBy, &e.ImportID, &fromFeed)
	if err != nil {
		return err
	}
	e.AllDay = allDay == 1
	e.FromFeed = fromFeed == 1
	e.StartsAt = starts.T
	e.EndsAt = ends.T
	return nil
}

// loadEvent reads one event of the family with its members and exceptions. It returns
// sql.ErrNoRows when the event is not in that family, so another family's event is never visible.
func loadEvent(ctx context.Context, q querier, familyID, id string) (event, error) {
	var e event
	row := q.QueryRowContext(ctx,
		`SELECT `+eventColumns+` FROM calendar_events WHERE id = ? AND family_id = ?`, id, familyID)
	if err := scanEvent(row, &e); err != nil {
		return e, err
	}

	members, err := loadMembers(ctx, q, familyID, id)
	if err != nil {
		return e, err
	}
	e.MemberIDs = members

	exceptions, err := loadExceptions(ctx, q, familyID, id)
	if err != nil {
		return e, err
	}
	e.Exceptions = exceptions

	reminders, err := loadReminders(ctx, q, familyID, id)
	if err != nil {
		return e, err
	}
	e.Reminders = reminders
	return e, nil
}

func loadMembers(ctx context.Context, q querier, familyID, eventID string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT family_member_id FROM calendar_event_members WHERE family_id = ? AND event_id = ? ORDER BY family_member_id`,
		familyID, eventID)
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

func loadExceptions(ctx context.Context, q querier, familyID, eventID string) ([]exception, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT occurrence_date, kind, title, description, location, starts_at, ends_at
		 FROM calendar_event_exceptions WHERE family_id = ? AND event_id = ? ORDER BY occurrence_date`,
		familyID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []exception{}
	for rows.Next() {
		var x exception
		var starts, ends *storedTime
		if err := rows.Scan(&x.OccurrenceDate, &x.Kind, &x.Title, &x.Description, &x.Location, &starts, &ends); err != nil {
			return nil, err
		}
		if starts != nil {
			x.StartsAt = starts.T
		}
		if ends != nil {
			x.EndsAt = ends.T
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// visibleToMemberSQL matches the events a family member may read: events assigned to the member,
// and events from an import for everyone. It takes the member ID as its one argument.
const visibleToMemberSQL = `(EXISTS (SELECT 1 FROM calendar_event_members m
	WHERE m.family_id = calendar_events.family_id AND m.event_id = calendar_events.id AND m.family_member_id = ?)
	OR EXISTS (SELECT 1 FROM calendar_imports i
	WHERE i.family_id = calendar_events.family_id AND i.id = calendar_events.import_id AND i.audience = 'everyone'))`

// isMember reports whether the event is shown to the family member: it is assigned to the member,
// or it comes from an import for everyone.
func isMember(ctx context.Context, q querier, familyID, eventID, memberID string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_events WHERE family_id = ? AND id = ? AND `+visibleToMemberSQL,
		familyID, eventID, memberID).Scan(&n)
	return n > 0, err
}

// series converts the event into the form Expand reads.
func (e event) series() (Series, error) {
	zone, err := time.LoadLocation(e.TimeZone)
	if err != nil {
		return Series{}, err
	}
	s := Series{
		ID:          e.ID,
		Title:       e.Title,
		Description: e.Description,
		Location:    e.Location,
		AllDay:      e.AllDay,
		Zone:        zone,
		StartDate:   e.StartDate,
	}
	if e.Rule != nil {
		s.Rule = *e.Rule
	}
	if e.AllDay {
		s.SpanDays = *e.SpanDays
		return s, nil
	}
	h, m, err := parseClock("start_time", *e.StartTime)
	if err != nil {
		return Series{}, err
	}
	s.StartHour, s.StartMinute = h, m
	s.DurationMinutes = *e.DurationMinutes
	return s, nil
}

// exceptionList converts the stored exceptions into the form Expand reads.
func (e event) exceptionList() []Exception {
	out := make([]Exception, 0, len(e.Exceptions))
	for _, x := range e.Exceptions {
		out = append(out, Exception{
			OccurrenceDate: x.OccurrenceDate,
			Cancelled:      x.Kind == "cancelled",
			Title:          derefString(x.Title),
			Description:    x.Description,
			Location:       x.Location,
			StartsAt:       x.StartsAt,
			EndsAt:         x.EndsAt,
		})
	}
	return out
}

// validateMembers checks that every member is an active member of the family. A FieldError is
// returned for a member that is not.
func validateMembers(ctx context.Context, q querier, familyID string, memberIDs []string) error {
	for _, id := range memberIDs {
		var n int
		if err := q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM family_members WHERE family_id = ? AND id = ? AND status = 'active'`,
			familyID, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fieldError("member_ids", "invalid")
		}
	}
	return nil
}

// defaultLocalColour is the colour of the Default calendar, and the one shown for any local
// calendar stored without a colour.
const defaultLocalColour = "#0D9488"

// ensureLocalSource returns the family's oldest local calendar, creating a Default one on first
// use. Events with no chosen calendar go there.
func ensureLocalSource(ctx context.Context, tx *sql.Tx, familyID string, now time.Time) (string, error) {
	stamp := dbTime(now)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO calendar_sources (id, family_id, kind, name, colour, created_at, updated_at)
		 SELECT ?, ?, 'local', 'Default', ?, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM calendar_sources WHERE family_id = ? AND kind = 'local')`,
		ids.New(), familyID, defaultLocalColour, stamp, stamp, familyID); err != nil {
		return "", err
	}
	var id string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM calendar_sources WHERE family_id = ? AND kind = 'local'
		 ORDER BY created_at, id LIMIT 1`, familyID).Scan(&id)
	return id, err
}

// localSourceExists reports whether id is one of the family's local calendars.
func localSourceExists(ctx context.Context, q querier, familyID, id string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_sources WHERE id = ? AND family_id = ? AND kind = 'local'`,
		id, familyID).Scan(&n)
	return n == 1, err
}

// calendarTarget is where an event is stored. Every event has a local calendar. An event on an
// imported or linked calendar also names that import, and its audience follows the import.
type calendarTarget struct {
	sourceID string
	importID *string
}

// importKind returns "file" or "link" for one of the family's imported calendars, or "" when there is none.
// A calendar is linked when it stores a source URL.
func importKind(ctx context.Context, q querier, familyID, id string) (string, error) {
	var kind string
	err := q.QueryRowContext(ctx,
		`SELECT CASE WHEN source_url_sealed IS NULL THEN 'file' ELSE 'link' END
		 FROM calendar_imports WHERE id = ? AND family_id = ?`,
		id, familyID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return kind, err
}

// chooseCalendar picks the calendar an event is saved to. A requested id can be a local calendar
// or an imported or linked one, and it must belong to the family. With none requested, the event
// keeps its current calendar, or goes to the default one when it is new.
func chooseCalendar(ctx context.Context, tx *sql.Tx, familyID string, requested *string, current calendarTarget, now time.Time) (calendarTarget, error) {
	if requested == nil || strings.TrimSpace(*requested) == "" {
		if current.sourceID != "" {
			return current, nil
		}
		sourceID, err := ensureLocalSource(ctx, tx, familyID, now)
		return calendarTarget{sourceID: sourceID}, err
	}
	id := strings.TrimSpace(*requested)
	if !isUUID(id) {
		return calendarTarget{}, fieldError("calendar_id", "invalid")
	}
	ok, err := localSourceExists(ctx, tx, familyID, id)
	if err != nil {
		return calendarTarget{}, err
	}
	if ok {
		return calendarTarget{sourceID: id}, nil
	}
	kind, err := importKind(ctx, tx, familyID, id)
	if err != nil {
		return calendarTarget{}, err
	}
	if kind == "" {
		return calendarTarget{}, fieldError("calendar_id", "invalid")
	}
	// Linked calendars are synced, and a sync replaces their events, so new events cannot go there.
	// An event already on that calendar may still be saved.
	if kind == "link" && (current.importID == nil || *current.importID != id) {
		return calendarTarget{}, fieldError("calendar_id", "invalid")
	}
	sourceID, err := ensureLocalSource(ctx, tx, familyID, now)
	if err != nil {
		return calendarTarget{}, err
	}
	return calendarTarget{sourceID: sourceID, importID: &id}, nil
}

// eventMemberIDs returns who an event is for. An event on an imported or linked calendar follows
// that calendar's audience, so the requested members are ignored.
func eventMemberIDs(ctx context.Context, q querier, familyID string, target calendarTarget, requested []string) ([]string, error) {
	if target.importID == nil {
		return requested, nil
	}
	return importMemberIDs(ctx, q, familyID, *target.importID)
}


// columns returns the event columns that depend on whether the event is all-day. A timed event
// has a start time and a duration; an all-day one has a span in days.
func (t timing) columns(allDay bool) (startTime *string, duration, span *int) {
	if allDay {
		s := t.spanDays
		return nil, nil, &s
	}
	st := t.startTime
	d := t.durationMinutes
	return &st, &d, nil
}

// insertEvent stores a new event. fromFeed marks events written by an ICS sync or file import,
// which are read-only in the app.
func insertEvent(ctx context.Context, tx *sql.Tx, familyID, userID, id string, target calendarTarget, fromFeed bool, v validatedEvent, now time.Time) error {
	startTime, duration, span := v.timing.columns(v.allDay)
	stamp := dbTime(now)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO calendar_events (
			id, family_id, source_id, title, description, location, all_day, time_zone,
			start_date, start_time, duration_minutes, span_days, starts_at, ends_at,
			recurrence_rule, version, created_by, created_at, updated_at, import_id, from_feed
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		id, familyID, target.sourceID, v.title, v.description, v.location, boolInt(v.allDay), v.zone.String(),
		v.timing.startDate, startTime, duration, span, dbTime(v.timing.startsAt), dbTime(v.timing.endsAt),
		v.rule, userID, stamp, stamp, target.importID, boolInt(fromFeed),
	)
	return err
}

// updateEvent replaces the event's fields if it is still at expectedVersion. It reports false when
// another write got there first.
func updateEvent(ctx context.Context, tx *sql.Tx, familyID, id string, expectedVersion int, target calendarTarget, v validatedEvent, now time.Time) (bool, error) {
	startTime, duration, span := v.timing.columns(v.allDay)
	res, err := tx.ExecContext(ctx,
		`UPDATE calendar_events SET
			source_id = ?, import_id = ?, title = ?, description = ?, location = ?, all_day = ?, time_zone = ?,
			start_date = ?, start_time = ?, duration_minutes = ?, span_days = ?,
			starts_at = ?, ends_at = ?, recurrence_rule = ?,
			version = version + 1, updated_at = ?
		WHERE id = ? AND family_id = ? AND version = ?`,
		target.sourceID, target.importID, v.title, v.description, v.location, boolInt(v.allDay), v.zone.String(),
		v.timing.startDate, startTime, duration, span,
		dbTime(v.timing.startsAt), dbTime(v.timing.endsAt), v.rule,
		dbTime(now), id, familyID, expectedVersion,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// bumpVersion advances the event's version after a change to one of its occurrences. It reports
// false when another write got there first.
func bumpVersion(ctx context.Context, tx *sql.Tx, familyID, id string, expectedVersion int, now time.Time) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE calendar_events SET version = version + 1, updated_at = ?
		 WHERE id = ? AND family_id = ? AND version = ?`,
		dbTime(now), id, familyID, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// replaceMembers sets the members the event is shown to. An empty list makes the event
// family-wide.
func replaceMembers(ctx context.Context, tx *sql.Tx, familyID, eventID string, memberIDs []string, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_event_members WHERE family_id = ? AND event_id = ?`, familyID, eventID); err != nil {
		return err
	}
	stamp := dbTime(now)
	for _, memberID := range memberIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO calendar_event_members (id, family_id, event_id, family_member_id, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			ids.New(), familyID, eventID, memberID, stamp); err != nil {
			return err
		}
	}
	return nil
}

// upsertException records the change to one occurrence. v is nil for a cancelled occurrence.
func upsertException(ctx context.Context, tx *sql.Tx, familyID, eventID, date string, v *validatedOccurrence, now time.Time) error {
	stamp := dbTime(now)
	var title, description, location, starts, ends *string
	kind := "cancelled"
	if v != nil {
		kind = "edited"
		title = &v.title
		description = v.description
		location = v.location
		s := dbTime(v.timing.startsAt)
		e := dbTime(v.timing.endsAt)
		starts, ends = &s, &e
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO calendar_event_exceptions (
			id, family_id, event_id, occurrence_date, kind, title, description, location,
			starts_at, ends_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (event_id, occurrence_date) DO UPDATE SET
			kind = excluded.kind, title = excluded.title, description = excluded.description,
			location = excluded.location, starts_at = excluded.starts_at, ends_at = excluded.ends_at,
			updated_at = excluded.updated_at`,
		ids.New(), familyID, eventID, date, kind, title, description, location, starts, ends, stamp, stamp)
	return err
}

// deleteExceptions removes every changed occurrence of the event.
func deleteExceptions(ctx context.Context, tx *sql.Tx, familyID, eventID string) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_event_exceptions WHERE family_id = ? AND event_id = ?`, familyID, eventID)
	return err
}

// deleteEvent removes an event with its members and exceptions. The version guard keeps a
// stale delete from removing a newer copy.
func deleteEvent(ctx context.Context, tx *sql.Tx, familyID, id string, expectedVersion int) (bool, error) {
	if err := deleteExceptions(ctx, tx, familyID, id); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_event_members WHERE family_id = ? AND event_id = ?`, familyID, id); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_events WHERE id = ? AND family_id = ? AND version = ?`, id, familyID, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
