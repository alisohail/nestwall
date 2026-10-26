package wall

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// eventStream is an open event stream read line by line.
type eventStream struct {
	lines chan string
}

// openStream connects the device to the event stream on a real server, because the stream needs a live
// connection that a recorder cannot provide.
func openStream(t *testing.T, f wallFixture, cookie *http.Cookie) *eventStream {
	t.Helper()
	srv := httptest.NewServer(f.mux)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/wall/events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open stream: status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content type %q, want text/event-stream", got)
	}

	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	return &eventStream{lines: lines}
}

// expectLine waits for a line equal to want.
func (s *eventStream) expectLine(t *testing.T, want string, within time.Duration) {
	t.Helper()
	timeout := time.After(within)
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				t.Fatalf("stream ended before %q", want)
			}
			if line == want {
				return
			}
		case <-timeout:
			t.Fatalf("no %q within %s", want, within)
		}
	}
}

// expectEnd waits for the server to end the stream. It fails if a change message arrives first.
func (s *eventStream) expectEnd(t *testing.T, within time.Duration) {
	t.Helper()
	timeout := time.After(within)
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				return
			}
			if line == "event: changed" {
				t.Fatal("stream reported a change for a revoked device")
			}
		case <-timeout:
			t.Fatalf("stream still open after %s", within)
		}
	}
}

func useFastStreamIntervals(t *testing.T) {
	t.Helper()
	poll, beat := eventsPollInterval, eventsHeartbeatInterval
	eventsPollInterval = 10 * time.Millisecond
	eventsHeartbeatInterval = 20 * time.Millisecond
	t.Cleanup(func() {
		eventsPollInterval, eventsHeartbeatInterval = poll, beat
	})
}

func TestEventsReportsDatabaseWrites(t *testing.T) {
	useFastStreamIntervals(t)
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	stream := openStream(t, f, cookie)
	stream.expectLine(t, "retry: 3000", 2*time.Second)

	if _, err := f.db.Exec(`UPDATE wall_devices SET name = 'Hall' WHERE id = ?`, dev.ID); err != nil {
		t.Fatalf("write: %v", err)
	}
	stream.expectLine(t, "event: changed", 2*time.Second)
}

func TestEventsSendsHeartbeat(t *testing.T) {
	useFastStreamIntervals(t)
	f := newWallFixture(t)
	_, cookie := enrolledDevice(t, f, "Kitchen")

	stream := openStream(t, f, cookie)
	stream.expectLine(t, ": ping", 2*time.Second)
}

func TestEventsEndWhenDeviceIsRevoked(t *testing.T) {
	useFastStreamIntervals(t)
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	stream := openStream(t, f, cookie)
	stream.expectLine(t, "retry: 3000", 2*time.Second)

	if rec := revokeDevice(f, dev.ID, f.parent); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status %d, body %s", rec.Code, rec.Body.String())
	}
	stream.expectEnd(t, 2*time.Second)

	if rec := send(f.mux, http.MethodGet, "/api/v1/wall/events", "", cookie, deviceRemote); rec.Code != http.StatusUnauthorized {
		t.Fatalf("reconnect after revoke: status %d, want 401", rec.Code)
	}
}

func TestEventsRefuseSessionAndAnonymousCallers(t *testing.T) {
	f := newWallFixture(t)
	enrolledDevice(t, f, "Kitchen")

	cases := map[string]*http.Cookie{
		"no cookie":      nil,
		"parent session": f.parent,
		"child session":  f.kid,
	}
	for name, cookie := range cases {
		rec := send(f.mux, http.MethodGet, "/api/v1/wall/events", "", cookie, deviceRemote)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "unauthenticated") {
			t.Errorf("%s: status %d, body %s; want 401 unauthenticated", name, rec.Code, rec.Body.String())
		}
	}
}
