package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"nestwall/backend/internal/platform/clock"
)

// DefaultBaseURL is the Open-Meteo forecast endpoint. It needs no API key.
const DefaultBaseURL = "https://api.open-meteo.com/v1/forecast"

const (
	cacheTTL         = 15 * time.Minute
	maxCacheEntries  = 512
	maxResponseBytes = 64 << 10
	requestTimeout   = 10 * time.Second
)

// ErrUnavailable means the forecast could not be fetched and no earlier answer is kept. The error
// never carries the coordinates.
var ErrUnavailable = errors.New("weather: forecast unavailable")

// Place is a location and the units to ask for.
type Place struct {
	Latitude  float64
	Longitude float64
	Unit      string
	Timezone  string
}

// Current is the conditions right now. IsDay tells the icon whether to show the sun or the moon.
type Current struct {
	Temperature float64 `json:"temperature"`
	FeelsLike   float64 `json:"feels_like"`
	Condition   string  `json:"condition"`
	IsDay       bool    `json:"is_day"`
}

// Day is the high, low and rain chance for one local day.
type Day struct {
	High         float64 `json:"high"`
	Low          float64 `json:"low"`
	Condition    string  `json:"condition"`
	PrecipChance *int    `json:"precip_chance"`
}

// Hour is the temperature, feels-like temperature and rain chance for one hour, starting at the hour that is current now.
type Hour struct {
	Time         time.Time `json:"time"`
	Temperature  float64   `json:"temperature"`
	FeelsLike    float64   `json:"feels_like"`
	Condition    string    `json:"condition"`
	IsDay        bool      `json:"is_day"`
	PrecipChance *int      `json:"precip_chance"`
}

// hourlyCount is how many hours the forecast carries, from the current hour onward.
const hourlyCount = 12

// Forecast is what the wall and the apps show. Stale means the provider could not be reached, so
// this is the last answer kept for the location.
type Forecast struct {
	Current   Current   `json:"current"`
	Today     Day       `json:"today"`
	Tomorrow  Day       `json:"tomorrow"`
	Hourly    []Hour    `json:"hourly"`
	FetchedAt time.Time `json:"fetched_at"`
	Stale     bool      `json:"stale"`
}

type cached struct {
	forecast  Forecast
	fetchedAt time.Time
}

// Client asks the forecast provider and keeps each answer for 15 minutes. When the provider fails,
// the last answer for the place is returned and marked stale.
type Client struct {
	baseURL string
	http    *http.Client
	clk     clock.Clock

	mu    sync.Mutex
	cache map[string]cached
}

// NewClient creates a client. An empty baseURL uses DefaultBaseURL.
func NewClient(baseURL string, clk clock.Clock) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: requestTimeout},
		clk:     clk,
		cache:   map[string]cached{},
	}
}

// Forecast returns the forecast for the place, from the cache when it is fresh.
func (c *Client) Forecast(ctx context.Context, p Place) (Forecast, error) {
	return c.load(ctx, p, false)
}

// Refresh fetches the forecast from upstream even when the cache is fresh, for an explicit
// refresh. The result still replaces the cached entry.
func (c *Client) Refresh(ctx context.Context, p Place) (Forecast, error) {
	return c.load(ctx, p, true)
}

func (c *Client) load(ctx context.Context, p Place, bypassCache bool) (Forecast, error) {
	key := fmt.Sprintf("%.4f|%.4f|%s|%s", p.Latitude, p.Longitude, p.Unit, p.Timezone)
	now := c.clk.Now()

	c.mu.Lock()
	entry, ok := c.cache[key]
	c.mu.Unlock()
	if ok && !bypassCache && now.Sub(entry.fetchedAt) < cacheTTL {
		return entry.forecast, nil
	}

	fresh, err := c.fetch(ctx, p, now)
	if err != nil {
		if ok {
			stale := entry.forecast
			stale.Stale = true
			return stale, nil
		}
		return Forecast{}, err
	}

	c.mu.Lock()
	c.evictLocked(now)
	c.cache[key] = cached{forecast: fresh, fetchedAt: now}
	c.mu.Unlock()
	return fresh, nil
}

// evictLocked keeps the cache bounded. Device coordinates add a key per location, so expired
// entries are dropped first, and the cache is reset if it is still full. Caller holds c.mu.
func (c *Client) evictLocked(now time.Time) {
	if len(c.cache) < maxCacheEntries {
		return
	}
	for k, e := range c.cache {
		if now.Sub(e.fetchedAt) >= cacheTTL {
			delete(c.cache, k)
		}
	}
	if len(c.cache) >= maxCacheEntries {
		c.cache = map[string]cached{}
	}
}

// upstream is the part of the Open-Meteo response that is used.
type upstream struct {
	Current struct {
		Temperature float64 `json:"temperature_2m"`
		FeelsLike   float64 `json:"apparent_temperature"`
		Code        int     `json:"weather_code"`
		IsDay       int     `json:"is_day"`
	} `json:"current"`
	Daily struct {
		Code   []int      `json:"weather_code"`
		High   []float64  `json:"temperature_2m_max"`
		Low    []float64  `json:"temperature_2m_min"`
		Precip []*float64 `json:"precipitation_probability_max"`
	} `json:"daily"`
	Hourly struct {
		Time   []string   `json:"time"`
		Temp   []float64  `json:"temperature_2m"`
		Feels  []float64  `json:"apparent_temperature"`
		Code   []int      `json:"weather_code"`
		IsDay  []int      `json:"is_day"`
		Precip []*float64 `json:"precipitation_probability"`
	} `json:"hourly"`
}

func (c *Client) fetch(ctx context.Context, p Place, now time.Time) (Forecast, error) {
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(p.Latitude, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(p.Longitude, 'f', 4, 64))
	q.Set("current", "temperature_2m,apparent_temperature,weather_code,is_day")
	q.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max")
	q.Set("hourly", "temperature_2m,apparent_temperature,weather_code,precipitation_probability,is_day")
	q.Set("timezone", p.Timezone)
	q.Set("forecast_days", "2")
	q.Set("temperature_unit", temperatureUnit(p.Unit))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"?"+q.Encode(), nil)
	if err != nil {
		return Forecast{}, ErrUnavailable
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The transport error can contain the URL, so it is not wrapped.
		return Forecast{}, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Forecast{}, ErrUnavailable
	}

	var body upstream
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&body); err != nil {
		return Forecast{}, ErrUnavailable
	}
	return body.forecast(now, p.Timezone)
}

// forecast converts the provider answer. It needs two days, and every daily list must have one
// value per day. Hourly data is optional: without it the forecast has no hours.
func (u upstream) forecast(now time.Time, zone string) (Forecast, error) {
	d := u.Daily
	if len(d.Code) < 2 || len(d.High) < 2 || len(d.Low) < 2 || len(d.Precip) < 2 {
		return Forecast{}, ErrUnavailable
	}
	return Forecast{
		Current: Current{
			Temperature: u.Current.Temperature,
			FeelsLike:   u.Current.FeelsLike,
			Condition:   conditionFor(u.Current.Code),
			IsDay:       u.Current.IsDay == 1,
		},
		Today:     day(d.Code[0], d.High[0], d.Low[0], d.Precip[0]),
		Tomorrow:  day(d.Code[1], d.High[1], d.Low[1], d.Precip[1]),
		Hourly:    u.hours(now, zone),
		FetchedAt: now,
	}, nil
}

// hours returns up to hourlyCount hours, starting with the hour that contains now. The provider
// times are local to the place, so they are read in the place's zone. Lists that do not line up
// give no hours rather than a wrong forecast.
func (u upstream) hours(now time.Time, zone string) []Hour {
	hr := u.Hourly
	out := []Hour{}
	n := len(hr.Time)
	if n == 0 || len(hr.Temp) != n || len(hr.Feels) != n || len(hr.Code) != n || len(hr.IsDay) != n || len(hr.Precip) != n {
		return out
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	for i, s := range hr.Time {
		t, err := time.ParseInLocation("2006-01-02T15:04", s, loc)
		if err != nil {
			return []Hour{}
		}
		if !t.Add(time.Hour).After(now) {
			continue
		}
		v := hr.Precip[i]
		var precip *int
		if v != nil {
			p := int(*v + 0.5)
			precip = &p
		}
		out = append(out, Hour{
			Time:         t,
			Temperature:  hr.Temp[i],
			FeelsLike:    hr.Feels[i],
			Condition:    conditionFor(hr.Code[i]),
			IsDay:        hr.IsDay[i] == 1,
			PrecipChance: precip,
		})
		if len(out) == hourlyCount {
			break
		}
	}
	return out
}

func day(code int, high, low float64, precip *float64) Day {
	out := Day{High: high, Low: low, Condition: conditionFor(code)}
	if precip != nil {
		v := int(*precip + 0.5)
		out.PrecipChance = &v
	}
	return out
}

func temperatureUnit(unit string) string {
	if unit == "f" {
		return "fahrenheit"
	}
	return "celsius"
}

// conditionFor maps a WMO weather code to the condition the animations use. Codes outside the
// known set map to "unknown", which shows a neutral icon.
func conditionFor(code int) string {
	switch code {
	case 0:
		return "clear"
	case 1, 2:
		return "partly_cloudy"
	case 3:
		return "cloudy"
	case 45, 48:
		return "fog"
	case 51, 53, 55, 56, 57:
		return "drizzle"
	case 61, 63, 65, 66, 67, 80, 81, 82:
		return "rain"
	case 71, 73, 75, 77, 85, 86:
		return "snow"
	case 95, 96, 99:
		return "storm"
	default:
		return "unknown"
	}
}
