package calendar

import (
	"testing"
	"time"
)

func TestBirthdayOccurrences(t *testing.T) {
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	mia := Birthday{MemberID: "m1", Name: "Mia", Birth: time.Date(2015, 3, 14, 0, 0, 0, 0, time.UTC)}
	leap := Birthday{MemberID: "m2", Name: "Leo", Birth: time.Date(2012, 2, 29, 0, 0, 0, 0, time.UTC)}

	day := func(s string) time.Time {
		d, err := time.Parse(dateLayout, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	t.Run("yearly birthday inside the window", func(t *testing.T) {
		got := BirthdayOccurrences([]Birthday{mia}, zone, day("2026-03-01"), day("2026-03-31"))
		if len(got) != 1 {
			t.Fatalf("want 1 occurrence, got %d", len(got))
		}
		if got[0].OccurrenceDate != "2026-03-14" || got[0].Name != "Mia" || got[0].MemberID != "m1" {
			t.Fatalf("unexpected occurrence %+v", got[0])
		}
		if got[0].StartsAt.In(zone).Format("2006-01-02 15:04") != "2026-03-14 00:00" {
			t.Fatalf("all-day birthday should start at family-local midnight, got %s", got[0].StartsAt)
		}
		if !got[0].EndsAt.Equal(got[0].StartsAt.AddDate(0, 0, 1)) {
			t.Fatalf("all-day birthday should last one day, got %s to %s", got[0].StartsAt, got[0].EndsAt)
		}
	})

	t.Run("birthday outside the window is left out", func(t *testing.T) {
		if got := BirthdayOccurrences([]Birthday{mia}, zone, day("2026-03-15"), day("2026-12-31")); len(got) != 0 {
			t.Fatalf("want none, got %+v", got)
		}
	})

	t.Run("window spanning a year end repeats the birthday each year", func(t *testing.T) {
		got := BirthdayOccurrences([]Birthday{mia}, zone, day("2026-03-01"), day("2027-03-31"))
		if len(got) != 2 || got[1].OccurrenceDate != "2027-03-14" {
			t.Fatalf("want 2026 and 2027 occurrences, got %+v", got)
		}
	})

	t.Run("29 February falls on 28 February in a non-leap year", func(t *testing.T) {
		got := BirthdayOccurrences([]Birthday{leap}, zone, day("2026-02-01"), day("2026-03-01"))
		if len(got) != 1 || got[0].OccurrenceDate != "2026-02-28" {
			t.Fatalf("want 2026-02-28, got %+v", got)
		}
	})

	t.Run("29 February falls on 29 February in a leap year", func(t *testing.T) {
		got := BirthdayOccurrences([]Birthday{leap}, zone, day("2028-02-01"), day("2028-03-01"))
		if len(got) != 1 || got[0].OccurrenceDate != "2028-02-29" {
			t.Fatalf("want 2028-02-29, got %+v", got)
		}
	})
}
