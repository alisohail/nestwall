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
	"strings"
	"sync"
	"time"

	"nestwall/backend/internal/platform/clock"
)

// DefaultNominatimURL is the OpenStreetMap reverse geocoding endpoint. It needs no API key, but its
// usage policy asks for an identifying User-Agent and light use, so lookups are cached.
const DefaultNominatimURL = "https://nominatim.openstreetmap.org/reverse"

const (
	placeCacheTTL   = 24 * time.Hour
	placeTimeout    = 5 * time.Second
	placeUserAgent  = "NestWall/1.0 (self-hosted family planner)"
	maxPlaceBytes   = 16 << 10
	maxPlaceEntries = 512
)

// ErrPlaceUnavailable means the place name could not be looked up. The error never carries the
// coordinates.
var ErrPlaceUnavailable = errors.New("weather: place name unavailable")

// PlaceNames turns coordinates into a short place name such as "Paris, France". Only device
// coordinates use it, rounded to about 1 km by the caller.
type PlaceNames struct {
	baseURL string
	http    *http.Client
	clk     clock.Clock

	mu    sync.Mutex
	cache map[string]placeEntry
}

type placeEntry struct {
	name      string
	fetchedAt time.Time
}

// NewPlaceNames creates a lookup. An empty baseURL uses DefaultNominatimURL.
func NewPlaceNames(baseURL string, clk clock.Clock) *PlaceNames {
	if baseURL == "" {
		baseURL = DefaultNominatimURL
	}
	return &PlaceNames{
		baseURL: baseURL,
		http:    &http.Client{Timeout: placeTimeout},
		clk:     clk,
		cache:   map[string]placeEntry{},
	}
}

// Name returns the place name for the coordinates, from the cache when it is fresh. Failed
// lookups are not cached, so the next request tries again.
func (p *PlaceNames) Name(ctx context.Context, latitude, longitude float64) (string, error) {
	key := strconv.FormatFloat(latitude, 'f', 2, 64) + "|" + strconv.FormatFloat(longitude, 'f', 2, 64)
	now := p.clk.Now()

	p.mu.Lock()
	entry, ok := p.cache[key]
	p.mu.Unlock()
	if ok && now.Sub(entry.fetchedAt) < placeCacheTTL {
		return entry.name, nil
	}

	name, err := p.fetch(ctx, latitude, longitude)
	if err != nil {
		return "", ErrPlaceUnavailable
	}

	p.mu.Lock()
	p.evictLocked(now)
	p.cache[key] = placeEntry{name: name, fetchedAt: now}
	p.mu.Unlock()
	return name, nil
}

// evictLocked keeps the cache bounded, the same way the forecast cache does. Caller holds p.mu.
func (p *PlaceNames) evictLocked(now time.Time) {
	if len(p.cache) < maxPlaceEntries {
		return
	}
	for k, e := range p.cache {
		if now.Sub(e.fetchedAt) >= placeCacheTTL {
			delete(p.cache, k)
		}
	}
	if len(p.cache) >= maxPlaceEntries {
		p.cache = map[string]placeEntry{}
	}
}

// nominatimAddress is the part of the reverse geocoding answer that is used.
type nominatimAddress struct {
	Address struct {
		City         string `json:"city"`
		Town         string `json:"town"`
		Village      string `json:"village"`
		Municipality string `json:"municipality"`
		County       string `json:"county"`
		State        string `json:"state"`
		Country      string `json:"country"`
	} `json:"address"`
}

func (p *PlaceNames) fetch(ctx context.Context, latitude, longitude float64) (string, error) {
	q := url.Values{}
	q.Set("format", "jsonv2")
	q.Set("zoom", "10")
	q.Set("lat", strconv.FormatFloat(latitude, 'f', 2, 64))
	q.Set("lon", strconv.FormatFloat(longitude, 'f', 2, 64))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"?"+q.Encode(), nil)
	if err != nil {
		return "", ErrPlaceUnavailable
	}
	req.Header.Set("User-Agent", placeUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		// The transport error can contain the URL, so it is not wrapped.
		return "", ErrPlaceUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ErrPlaceUnavailable
	}

	var body nominatimAddress
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPlaceBytes)).Decode(&body); err != nil {
		return "", ErrPlaceUnavailable
	}
	name := placeName(body)
	if name == "" {
		return "", ErrPlaceUnavailable
	}
	return name, nil
}

// placeName picks the most specific settlement name and adds the country. It returns "" when the
// answer has no usable name.
func placeName(body nominatimAddress) string {
	a := body.Address
	local := firstNonEmpty(a.City, a.Town, a.Village, a.Municipality, a.County, a.State)
	if local == "" {
		return ""
	}
	name := local
	if a.Country != "" && a.Country != local {
		name = fmt.Sprintf("%s, %s", local, a.Country)
	}
	if len(name) > 80 {
		return ""
	}
	return strings.TrimSpace(name)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
