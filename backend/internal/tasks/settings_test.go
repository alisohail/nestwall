package tasks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nestwall/backend/internal/platform/ids"
)

const settingsPath = "/api/v1/family/task-settings"

// builtInPartOfDayBody is the part-of-day body that matches the built-in times.
func builtInPartOfDayBody() map[string]any {
	return map[string]any{
		"morning_start":      "05:00",
		"after_school_start": "12:00",
		"evening_start":      "17:00",
	}
}

func TestGetSettingsReturnsBuiltInsAndETag(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.parent, http.MethodGet, settingsPath, nil, nil)
	expectStatus(t, rec, http.StatusOK)

	if etag := rec.Header().Get("ETag"); etag != `"1"` {
		t.Errorf("ETag = %q, want \"1\"", etag)
	}
	var got settingsResponse
	decodeInto(t, rec, &got)
	if got.PartOfDay.MorningStart != "05:00" || got.PartOfDay.AfterSchoolStart != "12:00" || got.PartOfDay.EveningStart != "17:00" {
		t.Errorf("part of day = %+v, want built-in times", got.PartOfDay)
	}
	if got.Defaults.Points != nil || got.Defaults.ApprovalRequired != nil || got.Defaults.MissedAfter != nil ||
		got.Defaults.PenaltyValue != nil || got.Defaults.PenaltyUnit != nil {
		t.Errorf("defaults = %+v, want all unset", got.Defaults)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
}

func TestGetSettingsNeedsActiveMembership(t *testing.T) {
	f := newFixture(t)
	stranger := actor{memberID: ids.New(), userID: f.parent.userID}
	rec := f.do(t, stranger, http.MethodGet, settingsPath, nil, nil)
	expectError(t, rec, http.StatusForbidden, "no_membership")
}

func TestUpdateSettingsStoresValuesAndBumpsVersion(t *testing.T) {
	f := newFixture(t)
	body := map[string]any{
		"defaults": map[string]any{
			"points":            3,
			"approval_required": true,
			"missed_after":      map[string]any{"value": 2, "unit": "weeks"},
			"penalty_value":     10,
			"penalty_unit":      "points",
		},
		"part_of_day": map[string]any{
			"morning_start":      "06:00",
			"after_school_start": "12:30",
			"evening_start":      "17:30",
		},
	}
	rec := f.do(t, f.parent, http.MethodPut, settingsPath, body, map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)
	if etag := rec.Header().Get("ETag"); etag != `"2"` {
		t.Errorf("ETag after update = %q, want \"2\"", etag)
	}

	var saved settingsResponse
	decodeInto(t, rec, &saved)
	if saved.Defaults.Points == nil || *saved.Defaults.Points != 3 {
		t.Errorf("saved points = %v, want 3", saved.Defaults.Points)
	}
	if saved.PartOfDay.MorningStart != "06:00" {
		t.Errorf("saved morning = %s, want 06:00", saved.PartOfDay.MorningStart)
	}
	if saved.Defaults.MissedAfter == nil || saved.Defaults.MissedAfter.Value != 2 || saved.Defaults.MissedAfter.Unit != "weeks" {
		t.Errorf("saved missed after = %+v, want 2 weeks", saved.Defaults.MissedAfter)
	}

	// The next GET returns the same values.
	get := f.do(t, f.parent, http.MethodGet, settingsPath, nil, nil)
	expectStatus(t, get, http.StatusOK)
	var again settingsResponse
	decodeInto(t, get, &again)
	if again.Version != 2 || again.PartOfDay.EveningStart != "17:30" {
		t.Errorf("reloaded = version %d evening %s, want 2 and 17:30", again.Version, again.PartOfDay.EveningStart)
	}
	if again.Defaults.PenaltyUnit == nil || *again.Defaults.PenaltyUnit != "points" {
		t.Errorf("reloaded penalty unit = %v, want points", again.Defaults.PenaltyUnit)
	}
	if again.Defaults.ApprovalRequired == nil || !*again.Defaults.ApprovalRequired {
		t.Errorf("reloaded approval = %v, want true", again.Defaults.ApprovalRequired)
	}
}

func TestUpdateSettingsWritesAuditRecord(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.parent, http.MethodPut, settingsPath, map[string]any{
		"defaults":    map[string]any{"points": 2},
		"part_of_day": builtInPartOfDayBody(),
	}, nil)
	expectStatus(t, rec, http.StatusOK)

	var n int
	var targetType string
	if err := f.db.QueryRow(
		`SELECT COUNT(*), COALESCE(MAX(target_type), '') FROM audit_logs WHERE family_id = ? AND action = 'task_settings.updated'`,
		f.a.FamilyID,
	).Scan(&n, &targetType); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if n != 1 || targetType != "family" {
		t.Fatalf("audit rows = %d target %q, want 1 row with target family", n, targetType)
	}
}

func TestUpdateSettingsEmptyDefaultsClearsStoredDefaults(t *testing.T) {
	f := newFixture(t)
	first := f.do(t, f.parent, http.MethodPut, settingsPath, map[string]any{
		"defaults":    map[string]any{"points": 2, "penalty_value": 5, "penalty_unit": "points"},
		"part_of_day": builtInPartOfDayBody(),
	}, nil)
	expectStatus(t, first, http.StatusOK)

	cleared := f.do(t, f.parent, http.MethodPut, settingsPath, map[string]any{
		"defaults":    map[string]any{},
		"part_of_day": builtInPartOfDayBody(),
	}, nil)
	expectStatus(t, cleared, http.StatusOK)

	var n int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM app_settings WHERE family_id = ? AND setting_key LIKE 'defaults.%'`, f.a.FamilyID,
	).Scan(&n); err != nil {
		t.Fatalf("count defaults: %v", err)
	}
	if n != 0 {
		t.Fatalf("stored default rows = %d, want 0 after clearing", n)
	}
}

func TestUpdateSettingsUsesVersion(t *testing.T) {
	f := newFixture(t)
	body := map[string]any{"defaults": map[string]any{}, "part_of_day": builtInPartOfDayBody()}

	rec := f.do(t, f.parent, http.MethodPut, settingsPath, body, map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)

	stale := f.do(t, f.parent, http.MethodPut, settingsPath, body, map[string]string{"If-Match": `"1"`})
	params := expectError(t, stale, http.StatusPreconditionFailed, "version_conflict")
	if params["version"] != float64(2) {
		t.Errorf("conflict version = %v, want 2", params["version"])
	}

	bad := f.do(t, f.parent, http.MethodPut, settingsPath, body, map[string]string{"If-Match": "nope"})
	expectError(t, bad, http.StatusBadRequest, "bad_request")
}

func TestUpdateSettingsValidation(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name   string
		body   map[string]any
		field  string
		reason string
	}{
		{
			name:   "single digit hour",
			body:   map[string]any{"defaults": map[string]any{}, "part_of_day": map[string]any{"morning_start": "5:00", "after_school_start": "12:00", "evening_start": "17:00"}},
			field:  "part_of_day",
			reason: "invalid_time",
		},
		{
			name:   "windows out of order",
			body:   map[string]any{"defaults": map[string]any{}, "part_of_day": map[string]any{"morning_start": "13:00", "after_school_start": "12:00", "evening_start": "17:00"}},
			field:  "part_of_day",
			reason: "out_of_order",
		},
		{
			name:   "penalty value without unit",
			body:   map[string]any{"defaults": map[string]any{"penalty_value": 5}, "part_of_day": builtInPartOfDayBody()},
			field:  "penalty",
			reason: "incomplete",
		},
		{
			name:   "percent over 100",
			body:   map[string]any{"defaults": map[string]any{"penalty_value": 120, "penalty_unit": "percent"}, "part_of_day": builtInPartOfDayBody()},
			field:  "penalty_value",
			reason: "over_100",
		},
		{
			name:   "negative points",
			body:   map[string]any{"defaults": map[string]any{"points": -1}, "part_of_day": builtInPartOfDayBody()},
			field:  "points",
			reason: "negative",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, f.parent, http.MethodPut, settingsPath, tc.body, nil)
			expectValidation(t, rec, tc.field, tc.reason)
		})
	}

	// Nothing was saved by the rejected requests.
	get := f.do(t, f.parent, http.MethodGet, settingsPath, nil, nil)
	var got settingsResponse
	decodeInto(t, get, &got)
	if got.Version != 1 {
		t.Errorf("version after rejected writes = %d, want 1", got.Version)
	}
}

func TestUpdateSettingsRejectsUnknownFields(t *testing.T) {
	f := newFixture(t)
	body := map[string]any{
		"defaults":    map[string]any{},
		"part_of_day": builtInPartOfDayBody(),
		"extra":       true,
	}
	rec := f.do(t, f.parent, http.MethodPut, settingsPath, body, nil)
	expectError(t, rec, http.StatusBadRequest, "bad_request")
}

func TestSettingsAreScopedToFamily(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.other, http.MethodPut, settingsPath, map[string]any{
		"defaults":    map[string]any{"points": 9},
		"part_of_day": builtInPartOfDayBody(),
	}, nil)
	expectStatus(t, rec, http.StatusOK)

	get := f.do(t, f.parent, http.MethodGet, settingsPath, nil, nil)
	expectStatus(t, get, http.StatusOK)
	if etag := get.Header().Get("ETag"); etag != `"1"` {
		t.Errorf("family A ETag = %q, want \"1\" after family B's write", etag)
	}
	var got settingsResponse
	decodeInto(t, get, &got)
	if got.Defaults.Points != nil {
		t.Errorf("family A points = %d, want unset", *got.Defaults.Points)
	}
}

func TestUpdateSettingsStoresBonusDefaults(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.parent, http.MethodPut, settingsPath, map[string]any{
		"defaults":    map[string]any{},
		"part_of_day": builtInPartOfDayBody(),
		"bonus_defaults": map[string]any{
			"points":            25,
			"claim_mode":        "everyone_once",
			"show_from":         "08:00",
			"ends":              "end_of_day",
			"approval_required": false,
		},
	}, map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)

	var saved settingsResponse
	decodeInto(t, rec, &saved)
	wantStoredBonusDefaults(t, saved.BonusDefaults)

	get := f.do(t, f.parent, http.MethodGet, settingsPath, nil, nil)
	expectStatus(t, get, http.StatusOK)
	var again settingsResponse
	decodeInto(t, get, &again)
	wantStoredBonusDefaults(t, again.BonusDefaults)
}

func wantStoredBonusDefaults(t *testing.T, b BonusDefaults) {
	t.Helper()
	if b.Points == nil || *b.Points != 25 {
		t.Errorf("bonus points = %v, want 25", b.Points)
	}
	if b.ClaimMode == nil || *b.ClaimMode != "everyone_once" {
		t.Errorf("bonus claim mode = %v, want everyone_once", b.ClaimMode)
	}
	if b.ShowFrom == nil || *b.ShowFrom != "08:00" {
		t.Errorf("bonus show from = %v, want 08:00", b.ShowFrom)
	}
	if b.Ends == nil || *b.Ends != EndOfDay {
		t.Errorf("bonus ends = %v, want %s", b.Ends, EndOfDay)
	}
	if b.ApprovalRequired == nil || *b.ApprovalRequired {
		t.Errorf("bonus approval = %v, want false", b.ApprovalRequired)
	}
}

func TestUpdateSettingsWithoutBonusDefaultsClearsThem(t *testing.T) {
	f := newFixture(t)
	first := f.do(t, f.parent, http.MethodPut, settingsPath, map[string]any{
		"defaults":       map[string]any{},
		"part_of_day":    builtInPartOfDayBody(),
		"bonus_defaults": map[string]any{"points": 25, "claim_mode": "first_come"},
	}, nil)
	expectStatus(t, first, http.StatusOK)

	cleared := f.do(t, f.parent, http.MethodPut, settingsPath, map[string]any{
		"defaults":    map[string]any{},
		"part_of_day": builtInPartOfDayBody(),
	}, nil)
	expectStatus(t, cleared, http.StatusOK)

	var got settingsResponse
	decodeInto(t, cleared, &got)
	if got.BonusDefaults.Points != nil || got.BonusDefaults.ClaimMode != nil {
		t.Fatalf("bonus defaults after clearing = %+v, want none", got.BonusDefaults)
	}
	var n int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM app_settings WHERE family_id = ? AND setting_key LIKE 'bonus_defaults.%'`, f.a.FamilyID,
	).Scan(&n); err != nil {
		t.Fatalf("count bonus rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("stored bonus rows = %d, want 0 after clearing", n)
	}
}

func TestUpdateSettingsBonusDefaultsValidation(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name   string
		bonus  map[string]any
		field  string
		reason string
	}{
		{"points zero", map[string]any{"points": 0}, "bonus_points", "out_of_range"},
		{"points too high", map[string]any{"points": 100001}, "bonus_points", "out_of_range"},
		{"unknown claim mode", map[string]any{"claim_mode": "weekly"}, "bonus_claim_mode", "invalid"},
		{"single digit show from", map[string]any{"show_from": "8:00"}, "bonus_show_from", "invalid_time"},
		{"ends not a time", map[string]any{"ends": "tomorrow"}, "bonus_ends", "invalid_time"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, f.parent, http.MethodPut, settingsPath, map[string]any{
				"defaults":       map[string]any{},
				"part_of_day":    builtInPartOfDayBody(),
				"bonus_defaults": tc.bonus,
			}, nil)
			expectValidation(t, rec, tc.field, tc.reason)
		})
	}

	get := f.do(t, f.parent, http.MethodGet, settingsPath, nil, nil)
	var got settingsResponse
	decodeInto(t, get, &got)
	if got.Version != 1 {
		t.Errorf("version after rejected bonus writes = %d, want 1", got.Version)
	}
}

// decodeInto decodes the response body into v.
func decodeInto(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}
