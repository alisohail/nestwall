package wall

import "testing"

// setLondon gives the family a location, so the wall can fetch a forecast.
func (s summaryFx) setLondon(t *testing.T) {
	t.Helper()
	mustExec(t, s.db,
		`UPDATE families SET latitude = 51.5, longitude = -0.12, location_name = 'London', temperature_unit = 'c' WHERE id = ?`,
		s.familyID)
}

func TestSummaryWeatherNotConfigured(t *testing.T) {
	s := newSummaryFx(t)
	out := s.fetch(t)
	if out.Weather == nil {
		t.Fatal("weather is missing; the wall shows weather by default")
	}
	if out.Weather.Configured || out.Weather.Forecast != nil {
		t.Fatalf("weather = %+v, want not configured and no forecast", out.Weather)
	}
}

func TestSummaryWeatherForecast(t *testing.T) {
	s := newSummaryFx(t)
	s.setLondon(t)
	out := s.fetch(t)
	w := out.Weather
	if w == nil || !w.Configured || w.Forecast == nil {
		t.Fatalf("weather = %+v, want a forecast", w)
	}
	if w.LocationName == nil || *w.LocationName != "London" {
		t.Fatalf("location_name = %v, want London", w.LocationName)
	}
	if w.TemperatureUnit != "c" {
		t.Fatalf("temperature_unit = %q, want c", w.TemperatureUnit)
	}
	if w.Forecast.Current.Condition != "cloudy" {
		t.Fatalf("current condition = %q, want cloudy", w.Forecast.Current.Condition)
	}
	if w.Forecast.Today.Condition != "rain" || w.Forecast.Tomorrow.Condition != "partly_cloudy" {
		t.Fatalf("today/tomorrow = %q/%q, want rain/partly_cloudy", w.Forecast.Today.Condition, w.Forecast.Tomorrow.Condition)
	}
}

func TestSummaryWeatherHiddenByDevice(t *testing.T) {
	s := newSummaryFx(t)
	s.setLondon(t)
	mustExec(t, s.db, `UPDATE wall_devices SET show_weather = 0 WHERE id = ?`, s.deviceID)
	if out := s.fetch(t); out.Weather != nil {
		t.Fatalf("weather = %+v, want none when the device hides it", out.Weather)
	}
}

func TestSummaryWeatherUpstreamDownKeepsSummary(t *testing.T) {
	s := newSummaryFx(t)
	s.setLondon(t)
	s.upstream.fail.Store(true)
	out := s.fetch(t)
	w := out.Weather
	if w == nil || !w.Configured || w.Forecast != nil {
		t.Fatalf("weather = %+v, want the location kept and no forecast", w)
	}
}
