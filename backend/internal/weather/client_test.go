package weather

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

const okBody = `{"current":{"temperature_2m":18.4,"apparent_temperature":17.1,"weather_code":3},` +
	`"daily":{"weather_code":[61,2],"temperature_2m_max":[21.5,22],"temperature_2m_min":[12.2,11],` +
	`"precipitation_probability_max":[70,null]}}`

// stub answers like Open-Meteo. It counts the calls and fails while fail is set. It keeps the
// latitude and longitude of the last request so tests can check what was sent upstream.
type stub struct {
	srv   *httptest.Server
	calls atomic.Int32
	fail  atomic.Bool
	mu    sync.Mutex
	lat   string
	lon   string
}

func newStub(t *testing.T) *stub {
	t.Helper()
	s := &stub{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		s.mu.Lock()
		s.lat, s.lon = r.URL.Query().Get("latitude"), r.URL.Query().Get("longitude")
		s.mu.Unlock()
		if s.fail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, okBody)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// sent returns the latitude and longitude of the last upstream request.
func (s *stub) sent() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lat, s.lon
}

var london = Place{Latitude: 51.5074, Longitude: -0.1278, Unit: "c", Timezone: "Europe/London"}

func TestClientReusesFreshAnswer(t *testing.T) {
	st := newStub(t)
	clk := &fakeClock{now: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	c := NewClient(st.srv.URL, clk)

	for i := 0; i < 3; i++ {
		f, err := c.Forecast(context.Background(), london)
		if err != nil {
			t.Fatalf("forecast %d: %v", i, err)
		}
		if f.Stale {
			t.Fatalf("forecast %d is stale on a healthy upstream", i)
		}
	}
	if got := st.calls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1 within the TTL", got)
	}

	clk.now = clk.now.Add(cacheTTL + time.Minute)
	if _, err := c.Forecast(context.Background(), london); err != nil {
		t.Fatalf("forecast after TTL: %v", err)
	}
	if got := st.calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2 after the TTL", got)
	}
}

func TestClientRefreshBypassesFreshCache(t *testing.T) {
	st := newStub(t)
	clk := &fakeClock{now: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	c := NewClient(st.srv.URL, clk)

	if _, err := c.Forecast(context.Background(), london); err != nil {
		t.Fatalf("forecast: %v", err)
	}
	if _, err := c.Refresh(context.Background(), london); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := st.calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2 because refresh skips the cache", got)
	}
	if _, err := c.Forecast(context.Background(), london); err != nil {
		t.Fatalf("forecast after refresh: %v", err)
	}
	if got := st.calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2 because refresh refills the cache", got)
	}
}

func TestClientServesStaleAnswerWhenUpstreamFails(t *testing.T) {
	st := newStub(t)
	clk := &fakeClock{now: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	c := NewClient(st.srv.URL, clk)
	if _, err := c.Forecast(context.Background(), london); err != nil {
		t.Fatalf("first forecast: %v", err)
	}

	clk.now = clk.now.Add(cacheTTL + time.Minute)
	st.fail.Store(true)
	f, err := c.Forecast(context.Background(), london)
	if err != nil {
		t.Fatalf("stale fallback returned error: %v", err)
	}
	if !f.Stale {
		t.Fatal("fallback answer is not marked stale")
	}
	if f.Today.Condition != "rain" {
		t.Fatalf("today condition = %q, want rain", f.Today.Condition)
	}
}

func TestClientUnavailableWithoutEarlierAnswer(t *testing.T) {
	st := newStub(t)
	st.fail.Store(true)
	c := NewClient(st.srv.URL, &fakeClock{now: time.Now()})

	_, err := c.Forecast(context.Background(), london)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), "51.5") || strings.Contains(err.Error(), st.srv.URL) {
		t.Fatalf("error leaks the location or URL: %q", err.Error())
	}
}

func TestClientRejectsShortForecast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"current":{"weather_code":0},"daily":{"weather_code":[0],"temperature_2m_max":[20],"temperature_2m_min":[10],"precipitation_probability_max":[0]}}`)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, &fakeClock{now: time.Now()})

	if _, err := c.Forecast(context.Background(), london); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable for a one-day answer", err)
	}
}

func TestConditionFor(t *testing.T) {
	cases := map[int]string{
		0: "clear", 2: "partly_cloudy", 3: "cloudy", 48: "fog", 55: "drizzle",
		61: "rain", 82: "rain", 73: "snow", 86: "snow", 95: "storm", 99: "storm",
		4: "unknown", 200: "unknown",
	}
	for code, want := range cases {
		if got := conditionFor(code); got != want {
			t.Errorf("conditionFor(%d) = %q, want %q", code, got, want)
		}
	}
}
