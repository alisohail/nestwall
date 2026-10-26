package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeClock struct {
	t time.Time
}

func (fc fakeClock) Now() time.Time {
	return fc.t
}

func TestHealthLive(t *testing.T) {
	fixedTime := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clk := fakeClock{t: fixedTime}

	h := NewHandler(clk, nil)
	handler := http.HandlerFunc(h.Live)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/health/live", nil)

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(recorder.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "UP" {
		t.Errorf("expected status UP, got %s", resp["status"])
	}

	expectedTime := fixedTime.Format("2006-01-02T15:04:05Z07:00")
	if resp["time"] != expectedTime {
		t.Errorf("expected time %s, got %s", expectedTime, resp["time"])
	}
}
