package calendar

import (
	"context"
	"database/sql"
	"time"
)

// Birthday is an active family member with a date of birth. Birthdays are not stored as events; they
// are worked out from the date of birth whenever the calendar or the wall is read.
type Birthday struct {
	MemberID string
	Name     string
	Birth    time.Time // UTC midnight of the date of birth
}

// BirthdayOccurrence is one member's birthday on a family-local date.
type BirthdayOccurrence struct {
	MemberID       string
	Name           string
	Age            int // the age the member turns on this day
	OccurrenceDate string
	StartsAt       time.Time
	EndsAt         time.Time
}

// LoadBirthdays returns the active members of the family who have a date of birth.
func LoadBirthdays(ctx context.Context, db *sql.DB, familyID string) ([]Birthday, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT fm.id, u.display_name, u.date_of_birth
		   FROM family_members fm
		   JOIN users u ON u.id = fm.user_id
		  WHERE fm.family_id = ? AND fm.status = 'active' AND u.date_of_birth IS NOT NULL AND u.date_of_birth <> ''
		  ORDER BY u.display_name, fm.id`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Birthday
	for rows.Next() {
		var b Birthday
		var dob string
		if err := rows.Scan(&b.MemberID, &b.Name, &dob); err != nil {
			return nil, err
		}
		birth, err := time.Parse(dateLayout, dob)
		if err != nil {
			// A stored value that does not parse is skipped rather than failing the whole calendar.
			continue
		}
		b.Birth = birth
		out = append(out, b)
	}
	return out, rows.Err()
}

// BirthdayOccurrences returns each member's birthday between first and last, both inclusive. first and
// last are UTC midnights of family-local dates, as the rest of the calendar uses them. A birthday on
// 29 February falls on 28 February in years without one.
func BirthdayOccurrences(members []Birthday, zone *time.Location, first, last time.Time) []BirthdayOccurrence {
	out := []BirthdayOccurrence{}
	for _, b := range members {
		for year := first.Year(); year <= last.Year(); year++ {
			day := time.Date(year, b.Birth.Month(), b.Birth.Day(), 0, 0, 0, 0, time.UTC)
			if b.Birth.Month() == time.February && b.Birth.Day() == 29 && day.Month() != time.February {
				day = day.AddDate(0, 0, -1)
			}
			if day.Before(first) || day.After(last) {
				continue
			}
			out = append(out, BirthdayOccurrence{
				MemberID:       b.MemberID,
				Name:           b.Name,
				Age:            year - b.Birth.Year(),
				OccurrenceDate: day.Format(dateLayout),
				StartsAt:       localMidnight(day, zone),
				EndsAt:         localMidnight(day.AddDate(0, 0, 1), zone),
			})
		}
	}
	return out
}
