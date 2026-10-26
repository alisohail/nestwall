package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/testhelper"
)

// putTheme calls PUT /api/v1/auth/me/theme with a session cookie.
func putTheme(t *testing.T, h *auth.Handlers, theme string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(map[string]string{"theme": theme}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/me/theme", &buf)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.SetTheme(rec, req)
	return rec
}

func TestThemeDefaultsToLightAndSavesChoice(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	h := newHandlers(db)
	sess, err := auth.CreateSession(t.Context(), db, clock.RealClock{}, fam.ParentUserID, "test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	cookie := &http.Cookie{Name: auth.SessionCookieName, Value: sess.Token}

	if n := countRows(t, db, `SELECT COUNT(*) FROM users WHERE id = ? AND theme = 'light'`, fam.ParentUserID); n != 1 {
		t.Fatal("new users should start on the light theme")
	}

	expectStatus(t, putTheme(t, h, "dark", cookie), http.StatusNoContent)
	if n := countRows(t, db, `SELECT COUNT(*) FROM users WHERE id = ? AND theme = 'dark'`, fam.ParentUserID); n != 1 {
		t.Fatal("dark was not saved")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(cookie)
	h.Me(rec, req)
	expectStatus(t, rec, http.StatusOK)
	var body struct {
		User struct {
			Theme string `json:"theme"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if body.User.Theme != "dark" {
		t.Fatalf("me theme = %q, want dark", body.User.Theme)
	}
}

func TestThemeRejectsUnknownValueAndNeedsSession(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	h := newHandlers(db)
	sess, err := auth.CreateSession(t.Context(), db, clock.RealClock{}, fam.ParentUserID, "test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	cookie := &http.Cookie{Name: auth.SessionCookieName, Value: sess.Token}

	expectStatus(t, putTheme(t, h, "neon", cookie), http.StatusBadRequest)
	if n := countRows(t, db, `SELECT COUNT(*) FROM users WHERE id = ? AND theme = 'light'`, fam.ParentUserID); n != 1 {
		t.Fatal("an invalid theme must not be saved")
	}

	expectStatus(t, putTheme(t, h, "light", &http.Cookie{Name: auth.SessionCookieName, Value: "nope"}), http.StatusUnauthorized)
}
