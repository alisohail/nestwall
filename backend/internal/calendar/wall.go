package calendar

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// WallEvent is one event occurrence the wall shows.
type WallEvent struct {
	Title          string
	OccurrenceDate string
	StartsAt       time.Time
	EndsAt         time.Time
	AllDay         bool
	Location       *string
	// MemberIDs are the members the event is shown to. Empty means the whole family.
	MemberIDs []string
	// Birthday is set for a member's birthday, where Title is the member's name and Age is the age reached.
	Birthday bool
	Age      int
}

// WallEvents returns the occurrences the wall shows between the first and last local dates, both
// inclusive, earliest first. An event is shown when its calendar is on the wall (an import with its
// wall switch on, or the family's local events) and it is family-wide or includes a child. Events
// that include only parents are left out.
func WallEvents(ctx context.Context, db *sql.DB, familyID string, zone *time.Location, first, last time.Time) ([]WallEvent, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT e.id FROM calendar_events e
		 WHERE e.family_id = ? AND e.start_date <= ?
		   AND (e.import_id IS NULL OR EXISTS (SELECT 1 FROM calendar_imports i
		        WHERE i.family_id = e.family_id AND i.id = e.import_id AND i.show_on_wall = 1))
		   AND (NOT EXISTS (SELECT 1 FROM calendar_event_members m
		        WHERE m.family_id = e.family_id AND m.event_id = e.id)
		        OR EXISTS (SELECT 1 FROM calendar_event_members m
		        JOIN family_members fm ON fm.family_id = m.family_id AND fm.id = m.family_member_id
		        JOIN roles r ON r.id = fm.role_id
		        WHERE m.family_id = e.family_id AND m.event_id = e.id AND r.name = 'Child' AND fm.status = 'active'))
		 ORDER BY e.start_date, e.id`,
		familyID, last.AddDate(0, 0, 2).Format(dateLayout))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	from := localMidnight(first, zone)
	to := localMidnight(last.AddDate(0, 0, 1), zone)
	out := []WallEvent{}
	for _, id := range ids {
		e, err := loadEvent(ctx, db, familyID, id)
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
			out = append(out, WallEvent{
				Title:          o.Title,
				OccurrenceDate: o.OccurrenceDate,
				StartsAt:       o.StartsAt,
				EndsAt:         o.EndsAt,
				AllDay:         o.AllDay,
				Location:       o.Location,
				MemberIDs:      e.MemberIDs,
			})
		}
	}
	birthdays, err := LoadBirthdays(ctx, db, familyID)
	if err != nil {
		return nil, err
	}
	for _, o := range BirthdayOccurrences(birthdays, zone, first, last) {
		out = append(out, WallEvent{
			Title:          o.Name,
			OccurrenceDate: o.OccurrenceDate,
			StartsAt:       o.StartsAt,
			EndsAt:         o.EndsAt,
			AllDay:         true,
			MemberIDs:      []string{o.MemberID},
			Birthday:       true,
			Age:            o.Age,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartsAt.Before(out[j].StartsAt) })
	return out, nil
}
