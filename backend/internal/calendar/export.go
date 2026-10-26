package calendar

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"

	"nestwall/backend/internal/platform/web"
)

const (
	exportFileName  = "nestwall-family.ics"
	exportProductID = "-//NestWall//Family planner//EN"
	exportUIDDomain = "nestwall"
	// exportYearsAhead is how far past the latest start the VTIMEZONE offsets are written. Repeating
	// events beyond it use the last offset written.
	exportYearsAhead = 10
	// icalDateTimeLayout is a floating local DATE-TIME, as VTIMEZONE onsets are written.
	icalDateTimeLayout = "20060102T150405"
)

// ExportFamily returns every event of the family as one iCalendar file. Only a caller who may read
// the whole calendar can export it.
func (h *Handlers) ExportFamily(w http.ResponseWriter, r *http.Request) {
	familyID, scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	if scope != "" {
		web.RespondError(w, http.StatusForbidden, "forbidden", "You do not have permission for this action", nil)
		return
	}
	data, err := h.buildCalendar(r.Context(), familyID, "")
	if err != nil {
		h.internalError(w, "export calendar", err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+exportFileName+`"`)
	writeICS(w, data)
}

func writeICS(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// buildCalendar renders the family's events that the scope may read as one iCalendar file. An empty
// scope includes every event. The family zone's VTIMEZONE is always present, so the file is valid
// even when it has no events.
func (h *Handlers) buildCalendar(ctx context.Context, familyID, scope string) ([]byte, error) {
	stamp := h.clock.Now().UTC()
	familyZone, _, err := h.familyZone(ctx, familyID)
	if err != nil {
		return nil, err
	}
	ids, err := h.scopedEvents(ctx, familyID, scope)
	if err != nil {
		return nil, err
	}

	type stored struct {
		event  event
		series Series
	}
	items := make([]stored, 0, len(ids))
	firstYear, lastYear := stamp.Year(), stamp.Year()
	for _, id := range ids {
		e, err := loadEvent(ctx, h.db, familyID, id)
		if err != nil {
			return nil, err
		}
		s, err := e.series()
		if err != nil {
			return nil, err
		}
		day, err := time.Parse(dateLayout, s.StartDate)
		if err != nil {
			return nil, err
		}
		firstYear = min(firstYear, day.Year())
		lastYear = max(lastYear, day.Year())
		items = append(items, stored{event: e, series: s})
	}

	zones := map[string]*time.Location{familyZone.String(): familyZone}
	for _, it := range items {
		if !it.series.AllDay && it.series.Zone.String() != time.UTC.String() {
			zones[it.series.Zone.String()] = it.series.Zone
		}
	}
	names := make([]string, 0, len(zones))
	for name := range zones {
		names = append(names, name)
	}
	sort.Strings(names)

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropProductID, exportProductID)
	cal.Props.SetText(ical.PropVersion, "2.0")
	for _, name := range names {
		cal.Children = append(cal.Children, timezoneComponent(zones[name], firstYear-1, lastYear+exportYearsAhead))
	}
	for _, it := range items {
		if err := appendEvent(cal, it.event, it.series, stamp); err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// scopedEvents returns the IDs of the family's events the scope may read, in start order.
func (h *Handlers) scopedEvents(ctx context.Context, familyID, scope string) ([]string, error) {
	conds := []string{"family_id = ?"}
	args := []any{familyID}
	if scope != "" {
		conds = append(conds, visibleToMemberSQL)
		args = append(args, scope)
	}
	rows, err := h.db.QueryContext(ctx,
		`SELECT id FROM calendar_events WHERE `+strings.Join(conds, " AND ")+` ORDER BY start_date, id`, args...)
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

// appendEvent adds the VEVENT for a series, a VEVENT for each edited occurrence, and an EXDATE on
// the series for each cancelled occurrence. Exceptions only apply to a repeating series, as Expand
// reads them.
func appendEvent(cal *ical.Calendar, e event, s Series, stamp time.Time) error {
	day, err := time.Parse(dateLayout, s.StartDate)
	if err != nil {
		return err
	}
	start, end := s.base(day)

	series := newVEvent(e.ID, stamp)
	series.Props.SetText(ical.PropSummary, s.Title)
	setOptionalText(series.Props, ical.PropDescription, s.Description)
	setOptionalText(series.Props, ical.PropLocation, s.Location)
	series.Props.Set(localProp(ical.PropDateTimeStart, start, s.Zone, s.AllDay))
	series.Props.Set(localProp(ical.PropDateTimeEnd, end, s.Zone, s.AllDay))
	cal.Children = append(cal.Children, series)
	if s.Rule == "" {
		return nil
	}

	rule := ical.NewProp(ical.PropRecurrenceRule)
	rule.Value = s.Rule
	series.Props.Set(rule)

	for _, x := range e.exceptionList() {
		occurrence, err := occurrenceStart(s, x.OccurrenceDate)
		if err != nil {
			return err
		}
		if x.Cancelled {
			series.Props.Add(localProp(ical.PropExceptionDates, occurrence, s.Zone, s.AllDay))
			continue
		}
		edit := newVEvent(e.ID, stamp)
		edit.Props.Set(localProp(ical.PropRecurrenceID, occurrence, s.Zone, s.AllDay))
		edit.Props.SetText(ical.PropSummary, x.Title)
		setOptionalText(edit.Props, ical.PropDescription, x.Description)
		setOptionalText(edit.Props, ical.PropLocation, x.Location)
		edit.Props.Set(localProp(ical.PropDateTimeStart, x.StartsAt, s.Zone, s.AllDay))
		edit.Props.Set(localProp(ical.PropDateTimeEnd, x.EndsAt, s.Zone, s.AllDay))
		cal.Children = append(cal.Children, edit)
	}
	return nil
}

// newVEvent returns a VEVENT with the identity every component of one event shares.
func newVEvent(eventID string, stamp time.Time) *ical.Component {
	c := ical.NewComponent(ical.CompEvent)
	c.Props.SetText(ical.PropUID, eventID+"@"+exportUIDDomain)
	c.Props.SetDateTime(ical.PropDateTimeStamp, stamp)
	return c
}

// occurrenceStart returns the instant the occurrence on the local date key starts at in the series.
func occurrenceStart(s Series, key string) (time.Time, error) {
	day, err := time.Parse(dateLayout, key)
	if err != nil {
		return time.Time{}, err
	}
	start, _ := s.base(day)
	return start, nil
}

// localProp writes an instant as a DATE for an all-day item, or as a DATE-TIME with its zone.
func localProp(name string, t time.Time, zone *time.Location, allDay bool) *ical.Prop {
	p := ical.NewProp(name)
	if allDay {
		p.SetDate(t.In(zone))
	} else {
		p.SetDateTime(t.In(zone))
	}
	return p
}

func setOptionalText(props ical.Props, name string, value *string) {
	if value != nil {
		props.SetText(name, *value)
	}
}

// timezoneComponent describes zone for the years fromYear to toYear. It has a component for the
// offset in force at the start and one for each later change. Each change is a one-off onset, so no
// RRULE is needed.
func timezoneComponent(zone *time.Location, fromYear, toYear int) *ical.Component {
	c := ical.NewComponent(ical.CompTimezone)
	c.Props.SetText(ical.PropTimezoneID, zone.String())

	cur := time.Date(fromYear, time.January, 1, 0, 0, 0, 0, zone)
	limit := time.Date(toYear, time.January, 1, 0, 0, 0, 0, zone)
	name, offset := cur.Zone()
	c.Children = append(c.Children, onset(cur, offset, offset, name, cur.IsDST()))
	for {
		_, next := cur.ZoneBounds()
		if next.IsZero() || !next.Before(limit) || !next.After(cur) {
			break
		}
		_, before := cur.Zone()
		name, after := next.Zone()
		c.Children = append(c.Children, onset(next, before, after, name, next.IsDST()))
		cur = next
	}
	return c
}

// onset returns the STANDARD or DAYLIGHT component that takes effect at when. RFC 5545 writes its
// DTSTART in the offset that applies before the change.
func onset(when time.Time, fromOffset, toOffset int, abbr string, dst bool) *ical.Component {
	kind := ical.CompTimezoneStandard
	if dst {
		kind = ical.CompTimezoneDaylight
	}
	c := ical.NewComponent(kind)
	start := ical.NewProp(ical.PropDateTimeStart)
	start.Value = when.In(time.FixedZone("", fromOffset)).Format(icalDateTimeLayout)
	c.Props.Set(start)
	c.Props.Set(offsetProp(ical.PropTimezoneOffsetFrom, fromOffset))
	c.Props.Set(offsetProp(ical.PropTimezoneOffsetTo, toOffset))
	c.Props.SetText(ical.PropTimezoneName, abbr)
	return c
}

func offsetProp(name string, seconds int) *ical.Prop {
	p := ical.NewProp(name)
	p.Value = formatUTCOffset(seconds)
	return p
}

// formatUTCOffset writes an offset as +hhmm, adding ss only when the offset has seconds.
func formatUTCOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign = "-"
		seconds = -seconds
	}
	h, m, s := seconds/3600, seconds%3600/60, seconds%60
	if s == 0 {
		return fmt.Sprintf("%s%02d%02d", sign, h, m)
	}
	return fmt.Sprintf("%s%02d%02d%02d", sign, h, m, s)
}
