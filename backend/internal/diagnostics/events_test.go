package diagnostics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nestwall/backend/internal/permissions"
)

func (f fixture) request(t *testing.T, method string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/settings/diagnostics/events", nil)
	ctx := context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, f.fam.MemberID)
	ctx = context.WithValue(ctx, permissions.ContextKeyUserID, f.fam.ParentUserID)
	rec := httptest.NewRecorder()
	handler(rec, req.WithContext(ctx))
	return rec
}

func countEvents(t *testing.T, f fixture) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM diagnostic_events`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestRecordKeepsOnlyTheNewestMaxEvents(t *testing.T) {
	f := newFixture(t)
	fam := f.fam.FamilyID
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < MaxEvents+5; i++ {
		if err := Record(context.Background(), f.db, &fam, KindEmail, "send_failed", nil, "", start.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	if got := countEvents(t, f); got != MaxEvents {
		t.Fatalf("rows = %d, want %d", got, MaxEvents)
	}
	var oldest time.Time
	if err := f.db.QueryRow(`SELECT created_at FROM diagnostic_events ORDER BY created_at ASC LIMIT 1`).Scan(&oldest); err != nil {
		t.Fatalf("oldest: %v", err)
	}
	if want := start.Add(5 * time.Minute); !oldest.Equal(want) {
		t.Fatalf("oldest kept = %v, want %v", oldest, want)
	}
}

func TestListEventsReturnsFamilyAndServerRowsNewestFirst(t *testing.T) {
	f := newFixture(t)
	fam := f.fam.FamilyID
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := Record(context.Background(), f.db, &fam, KindCalendarSync, "unavailable", nil, "", base); err != nil {
		t.Fatal(err)
	}
	if err := RecordServerErrorEvent(context.Background(), f.db, base.Add(time.Minute), "/api/v1/x", 500); err != nil {
		t.Fatal(err)
	}

	rec := f.request(t, http.MethodGet, f.h.ListEvents)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Events []Event `json:"events"`
		Max    int     `json:"max"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Max != MaxEvents || len(resp.Events) != 2 {
		t.Fatalf("got %d events (max %d), want 2", len(resp.Events), resp.Max)
	}
	first := resp.Events[0]
	if first.Kind != KindServerError || first.Code != "http_500" || first.Status == nil || *first.Status != 500 {
		t.Fatalf("first = %+v, want the 500 server error", first)
	}
	if first.Path == nil || *first.Path != "/api/v1/x" {
		t.Fatalf("path = %v, want /api/v1/x", first.Path)
	}
	if resp.Events[1].Code != "unavailable" || resp.Events[1].Path != nil {
		t.Fatalf("second = %+v, want the calendar event with no path", resp.Events[1])
	}
}

func TestListEventsHidesOtherFamiliesRows(t *testing.T) {
	f := newFixture(t)
	other := "other-family"
	mustExec(t, f.db, `INSERT INTO families (id, name, timezone, singleton_lock) VALUES (?, 'Other', 'UTC', NULL)`, other)
	if err := Record(context.Background(), f.db, &other, KindEmail, "send_failed", nil, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	rec := f.request(t, http.MethodGet, f.h.ListEvents)
	var resp struct {
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Events) != 0 {
		t.Fatalf("events = %+v, want none from another family", resp.Events)
	}
}

func TestClearEventsDeletesRowsResetsCounterAndAudits(t *testing.T) {
	f := newFixture(t)
	fam := f.fam.FamilyID
	if err := Record(context.Background(), f.db, &fam, KindEmail, "send_failed", nil, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := RecordServerErrorEvent(context.Background(), f.db, time.Now(), "/x", 502); err != nil {
		t.Fatal(err)
	}
	if serverErrorCount.Load() == 0 {
		t.Fatal("server error counter should be non-zero before clear")
	}

	rec := f.request(t, http.MethodDelete, f.h.ClearEvents)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Deleted int64 `json:"deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Deleted != 2 {
		t.Fatalf("deleted = %d, want 2", resp.Deleted)
	}
	if n := countEvents(t, f); n != 0 {
		t.Fatalf("rows left = %d, want 0", n)
	}
	if serverErrorCount.Load() != 0 || serverErrorLastNano.Load() != 0 {
		t.Fatal("server error counter should reset on clear")
	}

	var audits int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'diagnostics.events_cleared'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("audit rows = %d, want 1", audits)
	}
}
