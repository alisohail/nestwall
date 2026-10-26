package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

// fixture is one seeded parent with a current session.
type fixture struct {
	db      *sql.DB
	h       *Handlers
	fam     testhelper.Family
	current string // session ID of the signed-in session
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	sess, err := auth.ValidateSession(context.Background(), db, clock.RealClock{}, fam.SessionCookie.Value)
	if err != nil {
		t.Fatalf("validate seeded session: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return fixture{
		db:      db,
		h:       NewHandlers(db, clock.RealClock{}, logger),
		fam:     fam,
		current: sess.Session.ID,
	}
}

// addSession signs the parent in again and returns the new session ID.
func (f fixture) addSession(t *testing.T) string {
	t.Helper()
	s, err := auth.CreateSession(context.Background(), f.db, clock.RealClock{}, f.fam.ParentUserID, "test-agent")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return s.ID
}

// call runs a handler as the signed-in parent. pathID fills {id}.
func (f fixture) call(t *testing.T, fn http.HandlerFunc, pathID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetPathValue("id", pathID)
	ctx := req.Context()
	ctx = context.WithValue(ctx, permissions.ContextKeyUserID, f.fam.ParentUserID)
	ctx = context.WithValue(ctx, permissions.ContextKeySessionID, f.current)
	ctx = context.WithValue(ctx, permissions.ContextKeyFamilyMemberID, f.fam.MemberID)
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

func (f fixture) listOwn(t *testing.T) []SessionView {
	t.Helper()
	rec := f.call(t, f.h.ListOwn, "")
	expectStatus(t, rec, http.StatusOK)
	var resp ListSessionsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.Sessions
}

func TestListOwnMarksCurrentSession(t *testing.T) {
	f := newFixture(t)
	f.addSession(t)

	list := f.listOwn(t)
	if len(list) != 2 {
		t.Fatalf("sessions = %d, want 2", len(list))
	}
	currentCount := 0
	for _, s := range list {
		if s.Current {
			currentCount++
			if s.ID != f.current {
				t.Errorf("current flag on %s, want %s", s.ID, f.current)
			}
		}
	}
	if currentCount != 1 {
		t.Errorf("current sessions = %d, want 1", currentCount)
	}
}

func TestRevokeOwnSession(t *testing.T) {
	f := newFixture(t)
	other := f.addSession(t)

	expectStatus(t, f.call(t, f.h.RevokeOwn, other), http.StatusNoContent)
	// A second revoke of the same session finds nothing active.
	expectStatus(t, f.call(t, f.h.RevokeOwn, other), http.StatusNotFound)
	expectStatus(t, f.call(t, f.h.RevokeOwn, ids.New()), http.StatusNotFound)

	if n := len(f.listOwn(t)); n != 1 {
		t.Errorf("sessions after revoke = %d, want 1", n)
	}
}

func TestRevokeOthersKeepsCurrent(t *testing.T) {
	f := newFixture(t)
	f.addSession(t)
	f.addSession(t)

	expectStatus(t, f.call(t, f.h.RevokeOthers, ""), http.StatusNoContent)

	list := f.listOwn(t)
	if len(list) != 1 || list[0].ID != f.current {
		t.Fatalf("after revoke-others got %+v, want only current %s", list, f.current)
	}
}

func TestMemberSessionsAreScopedToFamily(t *testing.T) {
	f := newFixture(t)
	f.addSession(t)

	rec := f.call(t, f.h.ListForMember, f.fam.MemberID)
	expectStatus(t, rec, http.StatusOK)
	var resp ListSessionsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 2 {
		t.Errorf("member sessions = %d, want 2", len(resp.Sessions))
	}

	expectStatus(t, f.call(t, f.h.ListForMember, ids.New()), http.StatusNotFound)
}

func TestRevokeAllForMemberEndsEverySession(t *testing.T) {
	f := newFixture(t)
	f.addSession(t)

	expectStatus(t, f.call(t, f.h.RevokeAllForMember, f.fam.MemberID), http.StatusNoContent)

	var active int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE user_id = ? AND revoked_at IS NULL`, f.fam.ParentUserID,
	).Scan(&active); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if active != 0 {
		t.Errorf("active sessions after revoke-all = %d, want 0", active)
	}
	expectStatus(t, f.call(t, f.h.RevokeAllForMember, ids.New()), http.StatusNotFound)
}
