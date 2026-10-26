package notifications

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/permissions"
)

// The stream sends "changed" once the inbox changes, and nothing while it stays the same.
func TestStreamSignalsInboxChanges(t *testing.T) {
	f := newFixture(t)
	prevPoll := streamPollInterval
	streamPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { streamPollInterval = prevPoll })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), permissions.ContextKeyFamilyMemberID, f.fam.MemberID)
		f.h.Stream(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q, want text/event-stream", ct)
	}

	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()

	// Wait for the retry line, which the stream sends once it has taken its starting state.
	awaitLine(t, lines, "retry: ", "")

	f.insert(t, time.Now(), nil)
	awaitLine(t, lines, "event: changed", "after a new notice")
}

// awaitLine reads until a line with the prefix arrives, failing on timeout or stream end.
func awaitLine(t *testing.T, lines <-chan string, prefix, when string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed before %q %s", prefix, when)
			}
			if strings.HasPrefix(line, prefix) {
				return
			}
		case <-timeout:
			t.Fatalf("no %q line %s", prefix, when)
		}
	}
}
