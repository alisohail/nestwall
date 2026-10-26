package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) Response {
	t.Helper()
	var out Response
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestWeatherForecastUsesDeviceLocationWhenFamilyHasNone(t *testing.T) {
	f := newHandlerFixture(t)

	rec := httptest.NewRecorder()
	f.h.Forecast(rec, f.asParent(http.MethodGet, "/api/v1/weather?lat=48.8566&lon=2.3522", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	out := decodeResponse(t, rec)
	if !out.Configured || out.Source != "device" || out.Forecast == nil {
		t.Fatalf("response = %+v, want configured from the device with a forecast", out)
	}
	if out.LocationName == nil || *out.LocationName != "Paris, France" {
		t.Fatalf("location name = %v, want Paris, France", out.LocationName)
	}
	// Rounded to 2 decimals before the upstream call, so the exact device position is not sent.
	if lat, lon := f.st.sent(); lat != "48.8600" || lon != "2.3500" {
		t.Fatalf("upstream got %s,%s, want the rounded 48.86,2.35", lat, lon)
	}
}

func TestWeatherForecastDeviceLabelFallsBackToCoordinates(t *testing.T) {
	f := newHandlerFixture(t)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(down.Close)
	f.h.service.names = NewPlaceNames(down.URL, &fakeClock{now: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)})

	rec := httptest.NewRecorder()
	f.h.Forecast(rec, f.asParent(http.MethodGet, "/api/v1/weather?lat=48.8566&lon=2.3522", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	out := decodeResponse(t, rec)
	if out.Forecast == nil {
		t.Fatalf("response = %+v, want the forecast even without a place name", out)
	}
	if out.LocationName == nil || *out.LocationName != "48.86, 2.35" {
		t.Fatalf("location name = %v, want the rounded coordinates", out.LocationName)
	}
}

func TestWeatherForecastFamilyLocationWinsOverDevice(t *testing.T) {
	f := newHandlerFixture(t)
	f.setLondon(t)

	rec := httptest.NewRecorder()
	f.h.Forecast(rec, f.asParent(http.MethodGet, "/api/v1/weather?lat=48.8566&lon=2.3522", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	out := decodeResponse(t, rec)
	if out.Source != "family" || out.LocationName == nil || *out.LocationName != "London" {
		t.Fatalf("response = %+v, want the family location", out)
	}
	if lat, lon := f.st.sent(); lat != "51.5074" || lon != "-0.1278" {
		t.Fatalf("upstream got %s,%s, want the family coordinates", lat, lon)
	}
}

func TestWeatherForecastWithoutLocationOmitsSource(t *testing.T) {
	f := newHandlerFixture(t)

	rec := httptest.NewRecorder()
	f.h.Forecast(rec, f.asParent(http.MethodGet, "/api/v1/weather", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	out := decodeResponse(t, rec)
	if out.Configured || out.Source != "" {
		t.Fatalf("response = %+v, want no location and no source", out)
	}
}

func TestWeatherForecastDeviceLocationValidation(t *testing.T) {
	cases := []struct {
		name  string
		query string
		code  string
	}{
		{"latitude only", "?lat=48.8", "invalid_location"},
		{"longitude only", "?lon=2.3", "invalid_location"},
		{"latitude not a number", "?lat=abc&lon=2.3", "invalid_latitude"},
		{"latitude NaN", "?lat=NaN&lon=2.3", "invalid_latitude"},
		{"latitude out of range", "?lat=91&lon=2.3", "invalid_latitude"},
		{"longitude not a number", "?lat=48.8&lon=abc", "invalid_longitude"},
		{"longitude infinite", "?lat=48.8&lon=Inf", "invalid_longitude"},
		{"longitude out of range", "?lat=48.8&lon=181", "invalid_longitude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newHandlerFixture(t)
			rec := httptest.NewRecorder()
			f.h.Forecast(rec, f.asParent(http.MethodGet, "/api/v1/weather"+tc.query, ""))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got := errorCode(t, rec); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
			if n := f.st.calls.Load(); n != 0 {
				t.Fatalf("upstream calls = %d, want 0 for invalid input", n)
			}
		})
	}
}

func TestClientCacheStaysBounded(t *testing.T) {
	st := newStub(t)
	clk := &fakeClock{now: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	c := NewClient(st.srv.URL, clk)
	for i := 0; i < maxCacheEntries+10; i++ {
		p := Place{Latitude: float64(i) / 100, Longitude: 1, Unit: "c", Timezone: "UTC"}
		if _, err := c.Forecast(context.Background(), p); err != nil {
			t.Fatalf("forecast %d: %v", i, err)
		}
	}
	c.mu.Lock()
	n := len(c.cache)
	c.mu.Unlock()
	if n > maxCacheEntries {
		t.Fatalf("cache holds %d entries, want at most %d", n, maxCacheEntries)
	}
}
