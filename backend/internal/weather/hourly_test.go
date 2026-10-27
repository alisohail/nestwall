package weather

import (
	"encoding/json"
	"testing"
	"time"
)

func decodeUpstream(t *testing.T, body string) upstream {
	t.Helper()
	var u upstream
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return u
}

func hourlyBody(n int, temps, precip, codes string) string {
	times := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			times += ","
		}
		times += `"2026-10-27T` + twoDigits(10+i) + `:00"`
	}
	return `{"current":{"temperature_2m":18,"apparent_temperature":17,"weather_code":0},` +
		`"daily":{"weather_code":[0,1],"temperature_2m_max":[21,19],"temperature_2m_min":[12,11],` +
		`"precipitation_probability_max":[40,70]},` +
		`"hourly":{"time":[` + times + `],"temperature_2m":` + temps + `,` +
		`"weather_code":` + codes + `,"precipitation_probability":` + precip + `}}`
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func seq(n int, v string) string {
	s := "["
	for i := 0; i < n; i++ {
		if i > 0 {
			s += ","
		}
		s += v
	}
	return s + "]"
}

func TestHourlyStartsAtCurrentHourAndCapsAtTwelve(t *testing.T) {
	const zone = "America/Toronto"
	// 14 hours from 10:00 to 23:00 local. October 27 is on daylight time (UTC-4).
	body := hourlyBody(14, seq(14, "18.4"), seq(14, "null"), seq(14, "0"))
	u := decodeUpstream(t, body)
	now := time.Date(2026, 10, 27, 12, 30, 0, 0, mustLoc(t, zone))

	fc, err := u.forecast(now, zone)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	if len(fc.Hourly) != hourlyCount {
		t.Fatalf("hours = %d, want %d", len(fc.Hourly), hourlyCount)
	}
	if got := fc.Hourly[0].Time.In(mustLoc(t, zone)).Hour(); got != 12 {
		t.Fatalf("first hour = %d, want 12 (the hour containing now)", got)
	}
	if fc.Hourly[0].PrecipChance != nil {
		t.Fatalf("precip = %v, want nil when the provider sends null", *fc.Hourly[0].PrecipChance)
	}
}

func TestHourlyRoundsPrecipAndMapsCondition(t *testing.T) {
	const zone = "UTC"
	body := hourlyBody(3, "[1,2,3]", "[10.4,55.5,null]", "[61,0,95]")
	u := decodeUpstream(t, body)
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

	fc, err := u.forecast(now, zone)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	if len(fc.Hourly) != 3 {
		t.Fatalf("hours = %d, want 3", len(fc.Hourly))
	}
	if p := fc.Hourly[0].PrecipChance; p == nil || *p != 10 {
		t.Fatalf("first precip = %v, want 10", p)
	}
	if p := fc.Hourly[1].PrecipChance; p == nil || *p != 56 {
		t.Fatalf("second precip = %v, want 56", p)
	}
	if fc.Hourly[0].Condition != "rain" || fc.Hourly[1].Condition != "clear" || fc.Hourly[2].Condition != "storm" {
		t.Fatalf("conditions = %q %q %q", fc.Hourly[0].Condition, fc.Hourly[1].Condition, fc.Hourly[2].Condition)
	}
}

func TestHourlyMissingOrMismatchedGivesNoHours(t *testing.T) {
	const zone = "UTC"
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

	noHourly := `{"current":{"temperature_2m":18,"apparent_temperature":17,"weather_code":0},` +
		`"daily":{"weather_code":[0,1],"temperature_2m_max":[21,19],"temperature_2m_min":[12,11],` +
		`"precipitation_probability_max":[40,70]}}`
	fc, err := decodeUpstream(t, noHourly).forecast(now, zone)
	if err != nil {
		t.Fatalf("forecast without hourly: %v", err)
	}
	if fc.Hourly == nil || len(fc.Hourly) != 0 {
		t.Fatalf("hours without hourly data = %v, want an empty list", fc.Hourly)
	}

	mismatched := hourlyBody(3, "[1,2]", "[10,20,30]", "[0,0,0]")
	fc, err = decodeUpstream(t, mismatched).forecast(now, zone)
	if err != nil {
		t.Fatalf("forecast with mismatched hourly: %v", err)
	}
	if len(fc.Hourly) != 0 {
		t.Fatalf("hours with mismatched lists = %d, want 0", len(fc.Hourly))
	}
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}
