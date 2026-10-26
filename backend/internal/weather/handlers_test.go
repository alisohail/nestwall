package weather

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

const settingsPath = "/api/v1/family/weather-settings"

const londonBody = `{"location_name":"London","latitude":51.5074,"longitude":-0.1278,"temperature_unit":"c","animation":"calm"}`

// parisBody is what the place-name lookup answers for the device location in the tests.
const parisBody = `{"address":{"city":"Paris","country":"France"}}`

type handlerFixture struct {
	db  *sql.DB
	fam testhelper.Family
	h   *Handlers
	st  *stub
}

func newHandlerFixture(t *testing.T) handlerFixture {
	t.Helper()
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	st := newStub(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := &fakeClock{now: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	svc := NewService(db, NewClient(st.srv.URL, clk), logger)
	nom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, parisBody)
	}))
	t.Cleanup(nom.Close)
	svc.names = NewPlaceNames(nom.URL, clk)
	return handlerFixture{db: db, fam: fam, h: NewHandlers(db, svc, logger), st: st}
}

// request builds a request as the given member. An empty memberID means no one is signed in.
func request(method, target, body, memberID, userID string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if memberID != "" {
		ctx := context.WithValue(r.Context(), permissions.ContextKeyFamilyMemberID, memberID)
		ctx = context.WithValue(ctx, permissions.ContextKeyUserID, userID)
		r = r.WithContext(ctx)
	}
	return r
}

func (f handlerFixture) asParent(method, target, body string) *http.Request {
	return request(method, target, body, f.fam.MemberID, f.fam.ParentUserID)
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return out.Error.Code
}

func (f handlerFixture) getSettings(t *testing.T) Settings {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.GetSettings(rec, f.asParent(http.MethodGet, settingsPath, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("get settings status = %d, body %s", rec.Code, rec.Body.String())
	}
	var s Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return s
}

func (f handlerFixture) putSettings(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.h.UpdateSettings(rec, f.asParent(http.MethodPut, settingsPath, body))
	return rec
}

func (f handlerFixture) setLondon(t *testing.T) {
	t.Helper()
	if rec := f.putSettings(t, londonBody); rec.Code != http.StatusOK {
		t.Fatalf("set london status = %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestWeatherEndpointsRequireSignIn(t *testing.T) {
	f := newHandlerFixture(t)
	const validBody = `{"latitude":null,"longitude":null,"temperature_unit":"c","animation":"calm"}`
	cases := []struct {
		name   string
		method string
		body   string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{"forecast", http.MethodGet, "", f.h.Forecast},
		{"get settings", http.MethodGet, "", f.h.GetSettings},
		{"update settings", http.MethodPut, validBody, f.h.UpdateSettings},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.call(rec, request(tc.method, "/", tc.body, "", ""))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

func TestWeatherEndpointsRejectNonMembers(t *testing.T) {
	f := newHandlerFixture(t)
	stranger := ids.New()
	const validBody = `{"latitude":null,"longitude":null,"temperature_unit":"c","animation":"calm"}`
	cases := []struct {
		name   string
		method string
		body   string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{"forecast", http.MethodGet, "", f.h.Forecast},
		{"get settings", http.MethodGet, "", f.h.GetSettings},
		{"update settings", http.MethodPut, validBody, f.h.UpdateSettings},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.call(rec, request(tc.method, "/", tc.body, stranger, f.fam.ParentUserID))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestWeatherSettingsDefaultToNoLocationCelsius(t *testing.T) {
	f := newHandlerFixture(t)
	s := f.getSettings(t)
	if s.Latitude != nil || s.Longitude != nil || s.LocationName != nil {
		t.Fatalf("default location = %+v, want none", s)
	}
	if s.TemperatureUnit != "c" {
		t.Fatalf("default unit = %q, want c", s.TemperatureUnit)
	}
	if s.Animation != "calm" {
		t.Fatalf("default animation = %q, want calm", s.Animation)
	}
}

func TestWeatherSettingsRoundTrip(t *testing.T) {
	f := newHandlerFixture(t)
	rec := f.putSettings(t, `{"location_name":"  London  ","latitude":51.5074,"longitude":-0.1278,"temperature_unit":"f","animation":"live"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	s := f.getSettings(t)
	if s.LocationName == nil || *s.LocationName != "London" {
		t.Fatalf("location name = %v, want trimmed London", s.LocationName)
	}
	if s.Latitude == nil || *s.Latitude != 51.5074 || s.Longitude == nil || *s.Longitude != -0.1278 {
		t.Fatalf("coordinates = %v, %v", s.Latitude, s.Longitude)
	}
	if s.TemperatureUnit != "f" {
		t.Fatalf("unit = %q, want f", s.TemperatureUnit)
	}
	if s.Animation != "live" {
		t.Fatalf("animation = %q, want live", s.Animation)
	}
}

func TestWeatherSettingsClearWhenNoCoordinates(t *testing.T) {
	f := newHandlerFixture(t)
	f.setLondon(t)

	if rec := f.putSettings(t, `{"location_name":"Home","latitude":null,"longitude":null,"temperature_unit":"c","animation":"calm"}`); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body %s", rec.Code, rec.Body.String())
	}
	s := f.getSettings(t)
	if s.Latitude != nil || s.Longitude != nil || s.LocationName != nil {
		t.Fatalf("after clear = %+v, want no location", s)
	}
}

func TestWeatherSettingsValidation(t *testing.T) {
	f := newHandlerFixture(t)
	cases := []struct {
		name string
		body string
		code string
	}{
		{"latitude without longitude", `{"latitude":51.5,"longitude":null,"temperature_unit":"c","animation":"calm"}`, "invalid_location"},
		{"longitude without latitude", `{"latitude":null,"longitude":-0.1,"temperature_unit":"c","animation":"calm"}`, "invalid_location"},
		{"latitude out of range", `{"latitude":91,"longitude":0,"temperature_unit":"c","animation":"calm"}`, "invalid_latitude"},
		{"longitude out of range", `{"latitude":0,"longitude":-181,"temperature_unit":"c","animation":"calm"}`, "invalid_longitude"},
		{"name too long", `{"location_name":"` + strings.Repeat("a", 81) + `","latitude":1,"longitude":1,"temperature_unit":"c","animation":"calm"}`, "invalid_location_name"},
		{"unknown unit", `{"latitude":null,"longitude":null,"temperature_unit":"k","animation":"calm"}`, "invalid_temperature_unit"},
		{"unknown animation", `{"latitude":null,"longitude":null,"temperature_unit":"c","animation":"wild"}`, "invalid_animation"},
		{"missing animation", `{"latitude":null,"longitude":null,"temperature_unit":"c"}`, "invalid_animation"},
		{"malformed json", `{"latitude":`, "invalid_json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.putSettings(t, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got := errorCode(t, rec); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
		})
	}

	s := f.getSettings(t)
	if s.Latitude != nil || s.TemperatureUnit != "c" {
		t.Fatalf("rejected writes changed the family: %+v", s)
	}
}

func TestWeatherSettingsAuditOmitsCoordinates(t *testing.T) {
	f := newHandlerFixture(t)
	f.setLondon(t)

	var meta sql.NullString
	if err := f.db.QueryRow(`SELECT metadata FROM audit_logs WHERE action = 'family.weather_updated' AND family_id = ?`,
		f.fam.FamilyID).Scan(&meta); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if !meta.Valid || !strings.Contains(meta.String, "location_set") {
		t.Fatalf("audit metadata = %q, want location_set and unit", meta.String)
	}
	if strings.Contains(meta.String, "51.5") || strings.Contains(meta.String, "0.1278") {
		t.Fatalf("audit metadata leaks coordinates: %q", meta.String)
	}
}

func TestWeatherForecastWithoutLocation(t *testing.T) {
	f := newHandlerFixture(t)
	rec := httptest.NewRecorder()
	f.h.Forecast(rec, f.asParent(http.MethodGet, "/api/v1/weather", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var out Response
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Configured || out.Forecast != nil {
		t.Fatalf("response = %+v, want not configured with no forecast", out)
	}
	if n := f.st.calls.Load(); n != 0 {
		t.Fatalf("upstream calls = %d, want 0 without a location", n)
	}
}

func TestWeatherForecastWithLocation(t *testing.T) {
	f := newHandlerFixture(t)
	f.setLondon(t)

	rec := httptest.NewRecorder()
	f.h.Forecast(rec, f.asParent(http.MethodGet, "/api/v1/weather", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var out Response
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Configured || out.Forecast == nil {
		t.Fatalf("response = %+v, want configured with a forecast", out)
	}
	if out.Forecast.Current.Condition != "cloudy" || out.Forecast.Today.Condition != "rain" ||
		out.Forecast.Tomorrow.Condition != "partly_cloudy" {
		t.Fatalf("conditions = %+v", out.Forecast)
	}
}

func TestWeatherForecastUpstreamDownHidesCoordinates(t *testing.T) {
	f := newHandlerFixture(t)
	f.setLondon(t)
	f.st.fail.Store(true)

	rec := httptest.NewRecorder()
	f.h.Forecast(rec, f.asParent(http.MethodGet, "/api/v1/weather", ""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := errorCode(t, rec); got != "weather_unavailable" {
		t.Fatalf("code = %q, want weather_unavailable", got)
	}
	body := rec.Body.String()
	if strings.Contains(body, "51.5") || strings.Contains(body, f.st.srv.URL) {
		t.Fatalf("error leaks coordinates or upstream URL: %s", body)
	}
}