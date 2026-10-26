package tasks

import (
	"net/http"
	"testing"
	"time"
)

// torontoZone loads the zone the cut-off tests count in.
func torontoZone(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	return loc
}

// setCutoff saves the family settings with the given missed-after value and the built-in part of day.
func setCutoff(t *testing.T, f fixture, cutoff any) {
	t.Helper()
	rec := f.do(t, f.parent, http.MethodPut, settingsPath, map[string]any{
		"defaults":    map[string]any{"missed_after": cutoff},
		"part_of_day": builtInPartOfDayBody(),
	}, nil)
	expectStatus(t, rec, http.StatusOK)
}

func TestCutoffSettingRoundTripsAndClears(t *testing.T) {
	f := newFixture(t)
	setCutoff(t, f, map[string]any{"value": 3, "unit": "weeks"})

	rec := f.do(t, f.parent, http.MethodGet, settingsPath, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var got settingsResponse
	decodeInto(t, rec, &got)
	if got.Defaults.MissedAfter == nil || *got.Defaults.MissedAfter != (MissedAfter{Value: 3, Unit: unitWeeks}) {
		t.Fatalf("missed_after = %+v, want 3 weeks", got.Defaults.MissedAfter)
	}

	setCutoff(t, f, nil)
	rec = f.do(t, f.parent, http.MethodGet, settingsPath, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var cleared settingsResponse
	decodeInto(t, rec, &cleared)
	if cleared.Defaults.MissedAfter != nil {
		t.Fatalf("missed_after = %+v, want nil after clearing", cleared.Defaults.MissedAfter)
	}
	if n := recScalar(t, f.db, `SELECT COUNT(*) FROM app_settings WHERE family_id = ? AND setting_key LIKE 'defaults.missed_after%'`, f.a.FamilyID); n != "0" {
		t.Fatalf("stored cut-off rows = %s, want 0 after clearing", n)
	}
}

func TestCutoffSettingRejectsBadValues(t *testing.T) {
	f := newFixture(t)
	bad := []map[string]any{
		{"value": 0, "unit": "days"},
		{"value": 1000, "unit": "days"},
		{"value": 2, "unit": "hours"},
		{"value": 2, "unit": ""},
	}
	for _, cutoff := range bad {
		rec := f.do(t, f.parent, http.MethodPut, settingsPath, map[string]any{
			"defaults":    map[string]any{"missed_after": cutoff},
			"part_of_day": builtInPartOfDayBody(),
		}, nil)
		expectStatus(t, rec, http.StatusBadRequest)
	}
}

func TestSettingsRejectsRetiredExpiryField(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.parent, http.MethodPut, settingsPath, map[string]any{
		"defaults":    map[string]any{"expires_after_minutes": 1440},
		"part_of_day": builtInPartOfDayBody(),
	}, nil)
	expectStatus(t, rec, http.StatusBadRequest)
}

func TestOneOffWithoutGraceUsesCutoff(t *testing.T) {
	f := newFixture(t)
	execSQL(t, f.db, `UPDATE families SET timezone = 'America/Toronto' WHERE id = ?`, f.a.FamilyID)
	setCutoff(t, f, map[string]any{"value": 2, "unit": "days"})

	body := oneOffBody("Walk the dog", "2026-10-09", f.parent.memberID)
	body["due_time"] = "17:00"
	got := createTask(t, f, f.parent, body)
	if got.ExpiresAt == nil || *got.ExpiresAt != "2026-10-11T21:00:00Z" {
		t.Fatalf("expires_at = %v, want 2026-10-11T21:00:00Z (2 days after 17:00 Toronto)", got.ExpiresAt)
	}
}

func TestOneOffGraceOverridesCutoff(t *testing.T) {
	f := newFixture(t)
	execSQL(t, f.db, `UPDATE families SET timezone = 'America/Toronto' WHERE id = ?`, f.a.FamilyID)
	setCutoff(t, f, map[string]any{"value": 2, "unit": "days"})

	body := oneOffBody("Walk the dog", "2026-10-09", f.parent.memberID)
	body["due_time"] = "17:00"
	body["expires_after_minutes"] = 60
	got := createTask(t, f, f.parent, body)
	if got.ExpiresAt == nil || *got.ExpiresAt != "2026-10-09T22:00:00Z" {
		t.Fatalf("expires_at = %v, want 2026-10-09T22:00:00Z (60 minutes after due)", got.ExpiresAt)
	}
}

func TestOneOffWithoutCutoffNeverExpires(t *testing.T) {
	f := newFixture(t)
	got := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	if got.ExpiresAt != nil {
		t.Fatalf("expires_at = %v, want nil with no cut-off", *got.ExpiresAt)
	}
}

func TestUpdateAppliesCurrentCutoff(t *testing.T) {
	f := newFixture(t)
	execSQL(t, f.db, `UPDATE families SET timezone = 'America/Toronto' WHERE id = ?`, f.a.FamilyID)
	body := oneOffBody("Walk the dog", "2026-10-09", f.parent.memberID)
	body["due_time"] = "17:00"
	created := createTask(t, f, f.parent, body)
	if created.ExpiresAt != nil {
		t.Fatalf("expires_at = %v before cut-off set, want nil", *created.ExpiresAt)
	}

	setCutoff(t, f, map[string]any{"value": 1, "unit": "days"})
	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+created.ID,
		map[string]any{"title": "Walk the dog twice"}, map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if got.ExpiresAt == nil || *got.ExpiresAt != "2026-10-10T21:00:00Z" {
		t.Fatalf("expires_at = %v, want 2026-10-10T21:00:00Z after update", got.ExpiresAt)
	}
}

func TestMissedAfterExpiry(t *testing.T) {
	tor := torontoZone(t)
	cases := []struct {
		name   string
		cutoff MissedAfter
		due    time.Time
		want   time.Time
	}{
		{"days", MissedAfter{3, unitDays}, time.Date(2026, 10, 9, 17, 0, 0, 0, tor), time.Date(2026, 10, 12, 17, 0, 0, 0, tor)},
		{"weeks", MissedAfter{2, unitWeeks}, time.Date(2026, 10, 9, 17, 0, 0, 0, tor), time.Date(2026, 10, 23, 17, 0, 0, 0, tor)},
		{"months keep clock time", MissedAfter{1, unitMonths}, time.Date(2026, 10, 9, 17, 0, 0, 0, tor), time.Date(2026, 11, 9, 17, 0, 0, 0, tor)},
		{"Jan 31 plus a month clamps to Feb 28", MissedAfter{1, unitMonths}, time.Date(2026, 1, 31, 17, 0, 0, 0, tor), time.Date(2026, 2, 28, 17, 0, 0, 0, tor)},
		{"Oct 31 plus a month clamps to Nov 30", MissedAfter{1, unitMonths}, time.Date(2026, 10, 31, 17, 0, 0, 0, tor), time.Date(2026, 11, 30, 17, 0, 0, 0, tor)},
		{"Jan 31 plus a month in a leap year", MissedAfter{1, unitMonths}, time.Date(2028, 1, 31, 17, 0, 0, 0, tor), time.Date(2028, 2, 29, 17, 0, 0, 0, tor)},
		{"months roll over the year", MissedAfter{2, unitMonths}, time.Date(2026, 11, 30, 17, 0, 0, 0, tor), time.Date(2027, 1, 30, 17, 0, 0, 0, tor)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.cutoff.expiry(c.due, tor)
			if !got.Equal(c.want.UTC()) {
				t.Fatalf("expiry = %s, want %s", got, c.want.UTC())
			}
		})
	}
}

func TestOccurrenceExpiryPrefersGrace(t *testing.T) {
	tor := torontoZone(t)
	due := time.Date(2026, 10, 9, 17, 0, 0, 0, tor)
	grace := 60
	cutoff := MissedAfter{Value: 2, Unit: unitDays}

	if got := occurrenceExpiry(due, tor, &grace, &cutoff); got == nil || !got.Equal(due.Add(time.Hour)) {
		t.Fatalf("grace expiry = %v, want 60 minutes after due", got)
	}
	want := time.Date(2026, 10, 11, 17, 0, 0, 0, tor)
	if got := occurrenceExpiry(due, tor, nil, &cutoff); got == nil || !got.Equal(want) {
		t.Fatalf("cut-off expiry = %v, want %v", got, want)
	}
	if got := occurrenceExpiry(due, tor, nil, nil); got != nil {
		t.Fatalf("expiry = %v, want nil with no grace and no cut-off", *got)
	}
}

func TestMissedAfterValidate(t *testing.T) {
	ok := []MissedAfter{{1, unitDays}, {4, unitWeeks}, {999, unitMonths}}
	for _, m := range ok {
		if err := m.validate(); err != nil {
			t.Errorf("validate(%+v) = %v, want nil", m, err)
		}
	}
	bad := []MissedAfter{{0, unitDays}, {1000, unitDays}, {-1, unitWeeks}, {2, "hours"}, {2, ""}}
	for _, m := range bad {
		if err := m.validate(); err == nil {
			t.Errorf("validate(%+v) = nil, want error", m)
		}
	}
}

func TestGenerateWindowAppliesCutoff(t *testing.T) {
	conn, fam := recSetup(t)
	recExec(t, conn, `INSERT INTO app_settings (family_id, setting_key, setting_value) VALUES (?, 'defaults.missed_after_value', '1')`, fam.FamilyID)
	recExec(t, conn, `INSERT INTO app_settings (family_id, setting_key, setting_value) VALUES (?, 'defaults.missed_after_unit', 'weeks')`, fam.FamilyID)

	nine := "09:00"
	series := recSeries(t, conn, fam, "FREQ=DAILY", "2026-01-01", recZone, &nine)
	recGenerate(t, conn, fam, series, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))

	// Jan 3 09:00 Toronto plus one week is Jan 10 09:00 Toronto (EST, UTC-5), which is 14:00 UTC.
	want := dbTime(time.Date(2026, 1, 10, 9, 0, 0, 0, torontoZone(t)))
	if got := recScalar(t, conn,
		`SELECT CAST(expires_at AS TEXT) FROM task_occurrences WHERE task_definition_id = ? AND due_date = '2026-01-03'`, series); got != want {
		t.Errorf("expires_at = %s, want %s", got, want)
	}
}
