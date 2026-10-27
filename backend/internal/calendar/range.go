package calendar

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
	"nestwall/backend/internal/tasks"
)

// maxRangeDays is the longest window the range endpoint accepts, counting both ends.
const maxRangeDays = 366

// rangeItem is one thing happening in the range: an event occurrence or a task occurrence. Fields
// that only apply to one kind are omitted for the other.
type rangeItem struct {
	Key            string   `json:"key"`
	Kind           string   `json:"kind"`
	Title          string   `json:"title"`
	OccurrenceDate string   `json:"occurrence_date"`
	StartsAt       string   `json:"starts_at"`
	EndsAt         string   `json:"ends_at"`
	AllDay         bool     `json:"all_day"`
	TimeZone       string   `json:"time_zone"`
	MemberIDs      []string `json:"member_ids"`
	EventID        *string  `json:"event_id,omitempty"`
	CalendarID     *string  `json:"calendar_id,omitempty"`
	CalendarName   *string  `json:"calendar_name,omitempty"`
	CalendarColour *string  `json:"calendar_colour,omitempty"`
	Description    *string  `json:"description,omitempty"`
	Location       *string  `json:"location,omitempty"`
	Imported       bool     `json:"imported"`
	Reminders      []int    `json:"reminders,omitempty"`
	Recurring      *bool    `json:"recurring,omitempty"`
	Edited         *bool    `json:"edited,omitempty"`
	TaskID         *string  `json:"task_id,omitempty"`
	Status         *string  `json:"status,omitempty"`
}

// rangeCalendar is a calendar the caller can see at least one event from. Colour is empty for the
// family's local calendar, whose events take their colour from their members.
type rangeCalendar struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Colour string `json:"colour,omitempty"`
}

// rangeResponse is the answer to GET /api/v1/calendar/range. Items are sorted by start, then kind,
// then key, so the same request always gives the same list. Calendars lists every calendar the
// caller can see, whatever the window, so the screen can offer a filter for each one.
type rangeResponse struct {
	From      string          `json:"from"`
	To        string          `json:"to"`
	TimeZone  string          `json:"time_zone"`
	Items     []rangeItem     `json:"items"`
	Calendars []rangeCalendar `json:"calendars"`
}

// rangeQuery is a parsed and checked range request.
type rangeQuery struct {
	from       time.Time // UTC midnight of the first family-local date
	to         time.Time // UTC midnight of the last family-local date
	memberID   string
	calendarID string
	tasks      bool
}

// sortable pairs an item with its start instant so the list can be ordered before it is encoded.
type sortable struct {
	at   time.Time
	item rangeItem
}

// Range handles GET /api/v1/calendar/range. It returns the events and task occurrences that fall
// between from and to, both inclusive family-local dates. Event results follow calendar.view or
// calendar.view, and task results follow tasks.view. A caller without a task
// permission gets events only.
func (h *Handlers) Range(w http.ResponseWriter, r *http.Request) {
	familyID, scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	q, err := parseRangeQuery(r.URL.Query())
	if err != nil {
		h.rejectInvalid(w, "parse range", err)
		return
	}
	if err := h.checkRangeFilters(ctx, familyID, q); err != nil {
		h.rejectInvalid(w, "check range filters", err)
		return
	}

	zone, zoneName, err := h.familyZone(ctx, familyID)
	if err != nil {
		h.internalError(w, "load family zone", err)
		return
	}
	from := localMidnight(q.from, zone)
	to := localMidnight(q.to.AddDate(0, 0, 1), zone)

	entries, err := h.rangeEvents(ctx, familyID, scope, q, from, to)
	if err != nil {
		h.internalError(w, "load range events", err)
		return
	}

	// A task has no calendar, so a calendar filter leaves tasks out.
	taskScope, showTasks, err := h.taskScope(ctx)
	if err != nil {
		h.internalError(w, "check task scope", err)
		return
	}
	if showTasks && q.tasks && q.calendarID == "" {
		tasksEntries, err := h.rangeTasks(ctx, familyID, taskScope, q, zoneName)
		if err != nil {
			h.internalError(w, "load range tasks", err)
			return
		}
		entries = append(entries, tasksEntries...)
	}

	calendars, err := h.visibleCalendars(ctx, familyID, scope)
	if err != nil {
		h.internalError(w, "load range calendars", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, rangeResponse{
		From:      q.from.Format(dateLayout),
		To:        q.to.Format(dateLayout),
		TimeZone:  zoneName,
		Items:     sortItems(entries),
		Calendars: calendars,
	})
}

// visibleCalendars returns the calendars that have at least one event the scope may read, whatever
// the window, sorted by name then id. An empty scope means every event.
func (h *Handlers) visibleCalendars(ctx context.Context, familyID, scope string) ([]rangeCalendar, error) {
	conds := []string{"family_id = ?"}
	args := []any{familyID}
	if scope != "" {
		conds = append(conds, visibleToMemberSQL)
		args = append(args, scope)
	}
	ids, err := h.queryIDs(ctx,
		`SELECT DISTINCT COALESCE(import_id, source_id) FROM calendar_events WHERE `+strings.Join(conds, " AND "), args...)
	if err != nil {
		return nil, err
	}

	names, err := h.calendarNames(ctx, familyID)
	if err != nil {
		return nil, err
	}
	out := make([]rangeCalendar, 0, len(ids))
	for _, id := range ids {
		label := names[id]
		out = append(out, rangeCalendar{ID: id, Name: label.name, Colour: label.colour})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// parseRangeQuery reads and checks the date window and the plain parameters. Filters that need the
// database are checked by checkRangeFilters.
func parseRangeQuery(v url.Values) (rangeQuery, error) {
	var q rangeQuery
	from, err := parseDate("from", v.Get("from"))
	if err != nil {
		return q, err
	}
	to, err := parseDate("to", v.Get("to"))
	if err != nil {
		return q, err
	}
	if to.Before(from) {
		return q, fieldError("to", "before_from")
	}
	if int(to.Sub(from)/(24*time.Hour))+1 > maxRangeDays {
		return q, fieldError("to", "too_long")
	}
	q.from, q.to = from, to

	q.memberID = v.Get("member_id")
	if q.memberID != "" && !isUUID(q.memberID) {
		return q, fieldError("member_id", "invalid")
	}
	q.calendarID = v.Get("calendar_id")
	if q.calendarID != "" && !isUUID(q.calendarID) {
		return q, fieldError("calendar_id", "invalid")
	}

	switch v.Get("tasks") {
	case "", "true":
		q.tasks = true
	case "false":
		q.tasks = false
	default:
		return q, fieldError("tasks", "invalid")
	}
	return q, nil
}

// checkRangeFilters checks that a member or calendar filter names something in the family.
func (h *Handlers) checkRangeFilters(ctx context.Context, familyID string, q rangeQuery) error {
	if q.memberID != "" {
		var n int
		if err := h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM family_members WHERE family_id = ? AND id = ? AND status = 'active'`,
			familyID, q.memberID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fieldError("member_id", "invalid")
		}
	}
	if q.calendarID != "" {
		var n int
		if err := h.db.QueryRowContext(ctx,
			`SELECT (SELECT COUNT(*) FROM calendar_sources WHERE family_id = ? AND id = ?)
			      + (SELECT COUNT(*) FROM calendar_imports WHERE family_id = ? AND id = ?)`,
			familyID, q.calendarID, familyID, q.calendarID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fieldError("calendar_id", "invalid")
		}
	}
	return nil
}

// familyZone returns the family's time zone and its name.
func (h *Handlers) familyZone(ctx context.Context, familyID string) (*time.Location, string, error) {
	var name string
	if err := h.db.QueryRowContext(ctx,
		`SELECT timezone FROM families WHERE id = ?`, familyID).Scan(&name); err != nil {
		return nil, "", err
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		return nil, "", err
	}
	return zone, name, nil
}

// taskScope returns the member whose task copies the caller may see. An empty scope with show true
// means every occurrence. show is false when the caller has no task permission.
func (h *Handlers) taskScope(ctx context.Context) (scope string, show bool, err error) {
	memberID, _ := permissions.GetFamilyMemberID(ctx)
	policy := permissions.NewPolicy(h.db)
	all, err := policy.Check(ctx, memberID, permissions.PermTasksView)
	if err != nil {
		return "", false, err
	}
	if !all.Allowed {
		return "", false, nil
	}
	if all.AccessLevel != permissions.AccessOwn {
		return "", true, nil
	}
	return memberID, true, nil
}

// queryIDs runs a query that returns one text column and gives back the values.
func (h *Handlers) queryIDs(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// rangeEvents returns the occurrences of the family's events in the window. scope limits them to
// events the scope may read (see visibleToMemberSQL). The member filter limits them to events that
// include that member. The calendar filter limits them to one calendar.
func (h *Handlers) rangeEvents(ctx context.Context, familyID, scope string, q rangeQuery, from, to time.Time) ([]sortable, error) {
	conds := []string{"family_id = ?", "start_date <= ?"}
	args := []any{familyID, q.to.AddDate(0, 0, 2).Format(dateLayout)}
	if q.calendarID != "" {
		conds = append(conds, "COALESCE(import_id, source_id) = ?")
		args = append(args, q.calendarID)
	}
	if scope != "" {
		conds = append(conds, visibleToMemberSQL)
		args = append(args, scope)
	}
	if q.memberID != "" {
		conds = append(conds, `EXISTS (SELECT 1 FROM calendar_event_members m
			WHERE m.family_id = calendar_events.family_id AND m.event_id = calendar_events.id AND m.family_member_id = ?)`)
		args = append(args, q.memberID)
	}

	// An imported event belongs to its import; any other event belongs to the family's local source.
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, COALESCE(import_id, source_id) FROM calendar_events WHERE `+strings.Join(conds, " AND ")+
			` ORDER BY start_date, id`, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	calendarOf := map[string]string{}
	for rows.Next() {
		var id, calendar string
		if err := rows.Scan(&id, &calendar); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		calendarOf[id] = calendar
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	names, err := h.calendarNames(ctx, familyID)
	if err != nil {
		return nil, err
	}
	var out []sortable
	for _, id := range ids {
		e, err := loadEvent(ctx, h.db, familyID, id)
		if err != nil {
			return nil, err
		}
		s, err := e.series()
		if err != nil {
			return nil, err
		}
		occurrences, err := Expand(s, e.exceptionList(), from, to)
		if err != nil {
			return nil, err
		}
		for _, o := range occurrences {
			eventID, calendarID := o.EventID, calendarOf[id]
			label := names[calendarID]
			calendarName := label.name
			var calendarColour *string
			if label.colour != "" {
				calendarColour = &label.colour
			}
			recurring, edited := o.Recurring, o.Edited
			out = append(out, sortable{at: o.StartsAt, item: rangeItem{
				Key:            fmt.Sprintf("event:%s:%s", o.EventID, o.OccurrenceDate),
				Kind:           "event",
				Title:          o.Title,
				OccurrenceDate: o.OccurrenceDate,
				StartsAt:       o.StartsAt.UTC().Format(time.RFC3339),
				EndsAt:         o.EndsAt.UTC().Format(time.RFC3339),
				AllDay:         o.AllDay,
				TimeZone:       o.TimeZone,
				MemberIDs:      e.MemberIDs,
				EventID:        &eventID,
				CalendarID:     &calendarID,
				CalendarName:   &calendarName,
				CalendarColour: calendarColour,
				Description:    o.Description,
				Location:       o.Location,
				Imported:       e.FromFeed,
				Reminders:      e.Reminders,
				Recurring:      &recurring,
				Edited:         &edited,
			}})
		}
	}
	return out, nil
}

// calendarLabel is what the range shows for a calendar: its name, and its colour when it has one.
// Without a colour, the family's default calendar events take their colour from their members.
type calendarLabel struct {
	name   string
	colour string
}

// calendarNames maps each calendar id in the family to its label. A calendar is a source or an
// import; their ids are UUIDs, so they share one map.
func (h *Handlers) calendarNames(ctx context.Context, familyID string) (map[string]calendarLabel, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, name, COALESCE(colour, '') FROM calendar_sources WHERE family_id = ?
		 UNION ALL
		 SELECT id, name, colour FROM calendar_imports WHERE family_id = ?`, familyID, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]calendarLabel{}
	for rows.Next() {
		var id string
		var label calendarLabel
		if err := rows.Scan(&id, &label.name, &label.colour); err != nil {
			return nil, err
		}
		names[id] = label
	}
	return names, rows.Err()
}

// rangeTasks returns the task occurrences due in the window.
func (h *Handlers) rangeTasks(ctx context.Context, familyID, scope string, q rangeQuery, zoneName string) ([]sortable, error) {
	items, err := tasks.CalendarOccurrences(ctx, h.db, tasks.CalendarQuery{
		FamilyID:    familyID,
		From:        q.from.Format(dateLayout),
		To:          q.to.Format(dateLayout),
		ScopeMember: scope,
		MemberID:    q.memberID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]sortable, 0, len(items))
	for _, it := range items {
		taskID, status := it.OccurrenceID, it.Status
		out = append(out, sortable{at: it.DueAt, item: rangeItem{
			Key:            "task:" + it.OccurrenceID,
			Kind:           "task",
			Title:          it.Title,
			OccurrenceDate: it.DueDate,
			StartsAt:       it.DueAt.UTC().Format(time.RFC3339),
			EndsAt:         it.DueAt.UTC().Format(time.RFC3339),
			TimeZone:       zoneName,
			MemberIDs:      it.MemberIDs,
			TaskID:         &taskID,
			Status:         &status,
		}})
	}
	return out, nil
}

// sortItems orders the entries by start, then kind, then key, and drops any repeated key.
func sortItems(entries []sortable) []rangeItem {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if !a.at.Equal(b.at) {
			return a.at.Before(b.at)
		}
		if a.item.Kind != b.item.Kind {
			return a.item.Kind < b.item.Kind
		}
		return a.item.Key < b.item.Key
	})
	seen := make(map[string]bool, len(entries))
	out := make([]rangeItem, 0, len(entries))
	for _, e := range entries {
		if seen[e.item.Key] {
			continue
		}
		seen[e.item.Key] = true
		out = append(out, e.item)
	}
	return out
}
