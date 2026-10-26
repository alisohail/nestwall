package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlaceNameFormatting(t *testing.T) {
	cases := []struct {
		name string
		body nominatimAddress
		want string
	}{
		{"city and country", nominatimAddress{Address: struct {
			City         string `json:"city"`
			Town         string `json:"town"`
			Village      string `json:"village"`
			Municipality string `json:"municipality"`
			County       string `json:"county"`
			State        string `json:"state"`
			Country      string `json:"country"`
		}{City: "Paris", Country: "France"}}, "Paris, France"},
		{"town when no city", nominatimAddress{Address: struct {
			City         string `json:"city"`
			Town         string `json:"town"`
			Village      string `json:"village"`
			Municipality string `json:"municipality"`
			County       string `json:"county"`
			State        string `json:"state"`
			Country      string `json:"country"`
		}{Town: "Ledbury", Country: "United Kingdom"}}, "Ledbury, United Kingdom"},
		{"no settlement name", nominatimAddress{Address: struct {
			City         string `json:"city"`
			Town         string `json:"town"`
			Village      string `json:"village"`
			Municipality string `json:"municipality"`
			County       string `json:"county"`
			State        string `json:"state"`
			Country      string `json:"country"`
		}{Country: "France"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := placeName(tc.body); got != tc.want {
				t.Fatalf("placeName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlaceNamesCachesSuccessfulLookups(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "no user agent", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(parisBody))
	}))
	t.Cleanup(srv.Close)

	clk := &fakeClock{now: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	p := NewPlaceNames(srv.URL, clk)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		name, err := p.Name(ctx, 48.86, 2.35)
		if err != nil || name != "Paris, France" {
			t.Fatalf("Name = %q, %v; want Paris, France", name, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("upstream calls = %d, want 1 for repeated lookups", n)
	}

	// After the TTL the place is looked up again.
	clk.now = clk.now.Add(placeCacheTTL + time.Minute)
	if _, err := p.Name(ctx, 48.86, 2.35); err != nil {
		t.Fatalf("Name after TTL: %v", err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("upstream calls = %d, want 2 after the cache expired", n)
	}
}

func TestPlaceNamesFailureIsNotCached(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	p := NewPlaceNames(srv.URL, &fakeClock{now: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)})
	for i := 0; i < 2; i++ {
		if _, err := p.Name(context.Background(), 48.86, 2.35); err != ErrPlaceUnavailable {
			t.Fatalf("Name error = %v, want ErrPlaceUnavailable", err)
		}
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("upstream calls = %d, want 2 because failures are not cached", n)
	}
}

func TestPlaceNamesRejectsUnusableAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"address":{"country":"France"}}`))
	}))
	t.Cleanup(srv.Close)

	p := NewPlaceNames(srv.URL, &fakeClock{now: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)})
	if _, err := p.Name(context.Background(), 48.86, 2.35); err != ErrPlaceUnavailable {
		t.Fatalf("Name error = %v, want ErrPlaceUnavailable for an answer with no place", err)
	}
}
