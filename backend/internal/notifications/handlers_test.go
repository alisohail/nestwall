package notifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

type fixture struct {
	db  *sql.DB
	h   *Handlers
	fam testhelper.Family
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return fixture{db: db, h: NewHandlers(db, clock.RealClock{}, logger, "http://localhost:8080"), fam: fam}
}

// insert adds a notification addressed to the parent and returns its ID.
func (f fixture) insert(t *testing.T, createdAt time.Time, readAt *time.Time) string {
	t.Helper()
	id := ids.New()
	if _, err := f.db.ExecContext(context.Background(),
		`INSERT INTO notifications (id, family_id, recipient_id, kind, subject_member_id, body, read_at, created_at)
		 VALUES (?, ?, ?, 'password_reset_request', NULL, 'Test notification', ?, ?)`,
		id, f.fam.FamilyID, f.fam.MemberID, readAt, createdAt,
	); err != nil {
		t.Fatalf("insert notification: %v", err)
	}
	return id
}

// call runs a handler as the signed-in parent. pathID fills {id}.
func (f fixture) call(t *testing.T, fn http.HandlerFunc, pathID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetPathValue("id", pathID)
	ctx := context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, f.fam.MemberID)
	rec := httptest.NewRecorder()
	fn(rec, req.WithContext(ctx))
	return rec
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, want, rec.Body.String())
	}
}

func TestListShowsUnreadFirstWithCount(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()
	read := now.Add(-time.Hour)
	f.insert(t, now.Add(-2*time.Hour), nil)
	f.insert(t, now.Add(-time.Minute), nil)
	f.insert(t, now, &read)

	rec := f.call(t, f.h.List, "")
	expectStatus(t, rec, http.StatusOK)
	var resp ListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Notifications) != 3 {
		t.Fatalf("notifications = %d, want 3", len(resp.Notifications))
	}
	if resp.UnreadCount != 2 {
		t.Errorf("unread_count = %d, want 2", resp.UnreadCount)
	}
	if resp.Notifications[0].ReadAt != nil || resp.Notifications[1].ReadAt != nil {
		t.Errorf("unread notifications must come before read ones")
	}
	if resp.Notifications[2].ReadAt == nil {
		t.Errorf("last notification should be the read one")
	}
}

func TestMarkReadIsIdempotent(t *testing.T) {
	f := newFixture(t)
	id := f.insert(t, time.Now().UTC(), nil)

	expectStatus(t, f.call(t, f.h.MarkRead, id), http.StatusNoContent)
	var first sql.NullTime
	if err := f.db.QueryRow(`SELECT read_at FROM notifications WHERE id = ?`, id).Scan(&first); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !first.Valid {
		t.Fatalf("read_at not set after mark read")
	}

	// A second read succeeds and keeps the first read time.
	expectStatus(t, f.call(t, f.h.MarkRead, id), http.StatusNoContent)
	var second sql.NullTime
	if err := f.db.QueryRow(`SELECT read_at FROM notifications WHERE id = ?`, id).Scan(&second); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !second.Time.Equal(first.Time) {
		t.Errorf("read_at changed on second read: %v -> %v", first.Time, second.Time)
	}

	expectStatus(t, f.call(t, f.h.MarkRead, ids.New()), http.StatusNotFound)
}
