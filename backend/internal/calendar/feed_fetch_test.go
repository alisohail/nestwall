package calendar

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBlockedAddress(t *testing.T) {
	cases := []struct {
		ip    string
		allow bool // allowLoopback
		want  bool
	}{
		{"127.0.0.1", false, true},
		{"127.0.0.1", true, false},
		{"::1", false, true},
		{"0.0.0.0", false, true},
		{"10.0.0.1", true, true},
		{"172.16.5.4", false, true},
		{"192.168.1.10", false, true},
		{"169.254.169.254", false, true},
		{"100.64.0.1", false, true},
		{"0.1.2.3", false, true},
		{"240.0.0.1", false, true},
		{"255.255.255.255", false, true},
		{"224.0.0.1", false, true},
		{"fe80::1", false, true},
		{"fc00::1", false, true},
		{"64:ff9b::a00:1", false, true},     // NAT64 wrapping 10.0.0.1
		{"64:ff9b::808:808", false, false},  // NAT64 wrapping 8.8.8.8
		{"2002:0a00:0001::1", false, true},  // 6to4 wrapping 10.0.0.1
		{"2002:0808:0808::1", false, false}, // 6to4 wrapping 8.8.8.8
		{"93.184.216.34", false, false},
		{"8.8.8.8", false, false},
		{"2606:4700:4700::1111", false, false},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("bad test ip %q", tc.ip)
		}
		if got := blockedAddress(ip, tc.allow); got != tc.want {
			t.Errorf("blockedAddress(%s, allowLoopback=%v) = %v, want %v", tc.ip, tc.allow, got, tc.want)
		}
	}
}

func TestParseFeedURL(t *testing.T) {
	f := newFeedFetcher(false, time.Second)
	bad := map[string]error{
		"":                                  errInvalidURL,
		"   ":                               errInvalidURL,
		"example.com/cal.ics":               errInvalidURL,
		"https://":                          errInvalidURL,
		"ftp://example.com/cal.ics":         errLinkNotAllowed,
		"file:///etc/passwd":                errInvalidURL,
		"https://user:pw@example.com/a.ics": errLinkNotAllowed,
		"http://10.0.0.5/cal.ics":           errLinkNotAllowed,
		"http://127.0.0.1:8080/cal.ics":     errLinkNotAllowed,
		"http://169.254.169.254/latest":     errLinkNotAllowed,
		"https://" + strings.Repeat("a", maxFeedURLBytes) + ".com/x": errInvalidURL,
	}
	for raw, want := range bad {
		if _, err := f.parseFeedURL(raw); !errors.Is(err, want) {
			t.Errorf("parseFeedURL(%q) error = %v, want %v", raw, err, want)
		}
	}
	if _, err := f.parseFeedURL("https://calendar.example.com/feed.ics?token=abc"); err != nil {
		t.Errorf("valid link rejected: %v", err)
	}
}

func TestFetchServesBodyAndETag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("BEGIN:VCALENDAR"))
	}))
	defer srv.Close()

	f := newFeedFetcher(true, 5*time.Second)
	u, err := url.Parse(srv.URL + "/cal.ics")
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.fetch(context.Background(), u, "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if string(got.Body) != "BEGIN:VCALENDAR" || got.ETag != `"v1"` || got.NotModified {
		t.Fatalf("fetch result = %+v", got)
	}
}

func TestFetchNotModifiedWhenETagMatches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	f := newFeedFetcher(true, 5*time.Second)
	u, _ := url.Parse(srv.URL + "/cal.ics")
	got, err := f.fetch(context.Background(), u, `"v1"`)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !got.NotModified || got.ETag != `"v1"` || len(got.Body) != 0 {
		t.Fatalf("expected NotModified with the stored tag, got %+v", got)
	}
}

func TestFetchRefusesLoopbackWhenNotAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	// The dialer refuses the connection, so the request never reaches the server.
	f := newFeedFetcher(false, 5*time.Second)
	u, _ := url.Parse(srv.URL + "/cal.ics")
	_, err := f.fetch(context.Background(), u, "")
	if !errors.Is(err, errLinkNotAllowed) {
		t.Fatalf("fetch to loopback error = %v, want errLinkNotAllowed", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaks the address: %v", err)
	}
}

func TestFetchBlocksRedirectToPrivateAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/internal.ics", http.StatusFound)
	}))
	defer srv.Close()

	f := newFeedFetcher(true, 5*time.Second)
	u, _ := url.Parse(srv.URL + "/cal.ics")
	_, err := f.fetch(context.Background(), u, "")
	if !errors.Is(err, errLinkNotAllowed) {
		t.Fatalf("redirect to private address error = %v, want errLinkNotAllowed", err)
	}
}

func TestFetchStopsAfterTooManyRedirects(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srvURL+"/again", http.StatusFound)
	}))
	defer srv.Close()
	srvURL = srv.URL

	f := newFeedFetcher(true, 5*time.Second)
	u, _ := url.Parse(srv.URL + "/loop")
	_, err := f.fetch(context.Background(), u, "")
	if !errors.Is(err, errFeedUnavailable) {
		t.Fatalf("redirect loop error = %v, want errFeedUnavailable", err)
	}
}

func TestFetchRejectsServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	f := newFeedFetcher(true, 5*time.Second)
	u, _ := url.Parse(srv.URL + "/missing.ics")
	if _, err := f.fetch(context.Background(), u, ""); !errors.Is(err, errFeedUnavailable) {
		t.Fatalf("404 error = %v, want errFeedUnavailable", err)
	}
}

func TestFetchRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1<<20)
		for i := 0; i < 11; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	f := newFeedFetcher(true, 10*time.Second)
	u, _ := url.Parse(srv.URL + "/big.ics")
	if _, err := f.fetch(context.Background(), u, ""); !errors.Is(err, errFeedTooLarge) {
		t.Fatalf("oversized error = %v, want errFeedTooLarge", err)
	}
}

func TestFetchTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	f := newFeedFetcher(true, 100*time.Millisecond)
	u, _ := url.Parse(srv.URL + "/slow.ics")
	if _, err := f.fetch(context.Background(), u, ""); !errors.Is(err, errFeedUnavailable) {
		t.Fatalf("timeout error = %v, want errFeedUnavailable", err)
	}
}
