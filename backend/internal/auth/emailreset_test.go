package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/testhelper"
)

// fakeMailer records queued reset emails instead of sending them.
type fakeMailer struct {
	mu   sync.Mutex
	sent []string // bodies
	to   []string
	err  error
}

func (m *fakeMailer) Enqueue(_ context.Context, _, to, _, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.to = append(m.to, to)
	m.sent = append(m.sent, body)
	return nil
}

// tokenFromBody returns the raw token from the link in an emailed body.
func tokenFromBody(t *testing.T, body string) string {
	t.Helper()
	const marker = "/reset-password/"
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no reset link in body: %q", body)
	}
	rest := body[i+len(marker):]
	if j := strings.IndexAny(rest, "\n\r "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// setupResetFamily gives the parent an email and turns on SMTP for the family.
func setupResetFamily(t *testing.T, db *sql.DB, familyID, parentUserPK string, ready bool) {
	t.Helper()
	if _, err := db.Exec(`UPDATE users SET email = ? WHERE id = ?`, "parent@example.com", parentUserPK); err != nil {
		t.Fatalf("set email: %v", err)
	}
	if !ready {
		return
	}
	if _, err := db.Exec(
		`INSERT INTO smtp_settings (family_id, enabled, host, port, security, username, password_sealed,
		                            from_address, from_name, send_limit_per_hour, updated_at)
		 VALUES (?, 1, '127.0.0.1', 587, 'tls', 'mailer', 'sealed', 'nestwall@example.com', 'NestWall', 50,
		         '2026-10-21T00:00:00.000Z')`, familyID,
	); err != nil {
		t.Fatalf("enable smtp: %v", err)
	}
}

func newResetHandlers(db *sql.DB, mailer auth.ResetMailer) *auth.Handlers {
	clk := clock.RealClock{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return auth.NewHandlers(db, auth.NewRateLimiter(clk), clk, logger).WithEmailReset(mailer, "https://nestwall.example.com/")
}

func requestReset(t *testing.T, h *auth.Handlers, email string) *httptest.ResponseRecorder {
	t.Helper()
	return postJSON(t, h.ForgotPasswordByEmail, map[string]string{"email": email}, nil)
}

func completeReset(t *testing.T, h *auth.Handlers, token, password string) *httptest.ResponseRecorder {
	t.Helper()
	return postJSON(t, h.CompletePasswordReset, map[string]string{"token": token, "new_password": password}, nil)
}

func TestUserIDResetSendsLinkToParentEmail(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	setupResetFamily(t, db, fam.FamilyID, fam.ParentUserID, true)
	mailer := &fakeMailer{}
	h := newResetHandlers(db, mailer)

	rec := postJSON(t, h.ForgotPassword, map[string]string{"user_id": fam.ParentUserIDTx}, nil)
	expectStatus(t, rec, http.StatusAccepted)
	if len(mailer.sent) != 1 || mailer.to[0] != "parent@example.com" {
		t.Fatalf("emails = %d to %v, want one to the saved parent address", len(mailer.sent), mailer.to)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM password_reset_tokens WHERE user_id = ? AND used_at IS NULL`, fam.ParentUserID); n != 1 {
		t.Fatalf("stored unused tokens = %d, want 1", n)
	}
}

func TestUserIDResetSaysNotSetUpWhenFamilyIsNotReady(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	setupResetFamily(t, db, fam.FamilyID, fam.ParentUserID, false)
	mailer := &fakeMailer{}
	h := newResetHandlers(db, mailer)

	rec := postJSON(t, h.ForgotPassword, map[string]string{"user_id": fam.ParentUserIDTx}, nil)
	expectStatus(t, rec, http.StatusAccepted)
	if !strings.Contains(rec.Body.String(), "email_not_set_up") {
		t.Fatalf("body = %s, want email_not_set_up", rec.Body.String())
	}
	if len(mailer.sent) != 0 {
		t.Fatalf("emails queued = %d, want 0", len(mailer.sent))
	}
}

func TestEmailResetSendsOneLinkToTheParent(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	setupResetFamily(t, db, fam.FamilyID, fam.ParentUserID, true)
	mailer := &fakeMailer{}
	h := newResetHandlers(db, mailer)

	expectStatus(t, requestReset(t, h, "PARENT@example.com"), http.StatusAccepted)

	if len(mailer.sent) != 1 {
		t.Fatalf("emails queued = %d, want 1", len(mailer.sent))
	}
	if mailer.to[0] != "parent@example.com" {
		t.Fatalf("sent to %q", mailer.to[0])
	}
	if !strings.Contains(mailer.sent[0], "https://nestwall.example.com/reset-password/") {
		t.Fatalf("link not built from app URL: %q", mailer.sent[0])
	}
	token := tokenFromBody(t, mailer.sent[0])
	if n := countRows(t, db, `SELECT COUNT(*) FROM password_reset_tokens WHERE user_id = ? AND used_at IS NULL`, fam.ParentUserID); n != 1 {
		t.Fatalf("stored unused tokens = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM password_reset_tokens WHERE token_hash = ?`, auth.HashToken(token)); n != 1 {
		t.Fatal("stored token hash does not match the emailed token")
	}
}

func TestEmailResetSaysNotSetUpWhenNoFamilyIsReady(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	setupResetFamily(t, db, fam.FamilyID, fam.ParentUserID, false)
	mailer := &fakeMailer{}
	h := newResetHandlers(db, mailer)

	rec := requestReset(t, h, "parent@example.com")
	expectStatus(t, rec, http.StatusAccepted)
	if !strings.Contains(rec.Body.String(), "email_not_set_up") {
		t.Fatalf("body = %s, want email_not_set_up", rec.Body.String())
	}
	if len(mailer.sent) != 0 {
		t.Fatal("email was queued although SMTP is not set up")
	}
}

func TestEmailResetNeverEmailsChildren(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	setupResetFamily(t, db, fam.FamilyID, fam.ParentUserID, true)
	kidLogin, _ := addChild(t, db, fam.FamilyID)
	if _, err := db.Exec(`UPDATE users SET email = ? WHERE user_id = ?`, "kid@example.com", kidLogin); err != nil {
		t.Fatalf("set child email: %v", err)
	}
	mailer := &fakeMailer{}
	h := newResetHandlers(db, mailer)

	expectStatus(t, requestReset(t, h, "kid@example.com"), http.StatusAccepted)
	if len(mailer.sent) != 0 {
		t.Fatal("a child received a reset email")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM password_reset_tokens`); n != 0 {
		t.Fatalf("tokens stored for a child = %d, want 0", n)
	}
}

func TestEmailResetNewerLinkReplacesOlder(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	setupResetFamily(t, db, fam.FamilyID, fam.ParentUserID, true)
	mailer := &fakeMailer{}
	h := newResetHandlers(db, mailer)

	requestReset(t, h, "parent@example.com")
	requestReset(t, h, "parent@example.com")
	if len(mailer.sent) != 2 {
		t.Fatalf("emails = %d, want 2", len(mailer.sent))
	}
	older := tokenFromBody(t, mailer.sent[0])
	newer := tokenFromBody(t, mailer.sent[1])

	expectStatus(t, completeReset(t, h, older, newPassword), http.StatusBadRequest)
	expectStatus(t, completeReset(t, h, newer, newPassword), http.StatusNoContent)
}

func TestEmailResetLinkWorksOnceAndSignsOutSessions(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	setupResetFamily(t, db, fam.FamilyID, fam.ParentUserID, true)
	if _, err := db.Exec(`UPDATE users SET must_change_password = 1 WHERE id = ?`, fam.ParentUserID); err != nil {
		t.Fatalf("set must_change: %v", err)
	}
	mailer := &fakeMailer{}
	h := newResetHandlers(db, mailer)
	requestReset(t, h, "parent@example.com")
	token := tokenFromBody(t, mailer.sent[0])

	expectStatus(t, completeReset(t, h, token, newPassword), http.StatusNoContent)
	expectStatus(t, completeReset(t, h, token, newPassword), http.StatusBadRequest)

	if n := countRows(t, db, `SELECT COUNT(*) FROM users WHERE id = ? AND must_change_password = 0`, fam.ParentUserID); n != 1 {
		t.Fatal("must_change_password was not cleared")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM sessions WHERE user_id = ? AND revoked_at IS NULL`, fam.ParentUserID); n != 0 {
		t.Fatalf("active sessions after reset = %d, want 0", n)
	}
}

func TestEmailResetWeakPasswordKeepsLinkUsable(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	setupResetFamily(t, db, fam.FamilyID, fam.ParentUserID, true)
	mailer := &fakeMailer{}
	h := newResetHandlers(db, mailer)
	requestReset(t, h, "parent@example.com")
	token := tokenFromBody(t, mailer.sent[0])

	expectStatus(t, completeReset(t, h, token, "short"), http.StatusBadRequest)
	expectStatus(t, completeReset(t, h, token, newPassword), http.StatusNoContent)
}

func TestEmailResetExpiredLinkIsRejected(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	setupResetFamily(t, db, fam.FamilyID, fam.ParentUserID, true)
	mailer := &fakeMailer{}
	h := newResetHandlers(db, mailer)
	requestReset(t, h, "parent@example.com")
	token := tokenFromBody(t, mailer.sent[0])

	if _, err := db.Exec(`UPDATE password_reset_tokens SET expires_at = '2000-01-01T00:00:00Z' WHERE user_id = ?`, fam.ParentUserID); err != nil {
		t.Fatalf("expire link: %v", err)
	}
	rec := completeReset(t, h, token, newPassword)
	expectStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "reset_link_invalid") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestEmailResetFailedEnqueueStoresNothing(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	setupResetFamily(t, db, fam.FamilyID, fam.ParentUserID, true)
	mailer := &fakeMailer{err: errors.New("queue down")}
	h := newResetHandlers(db, mailer)

	expectStatus(t, requestReset(t, h, "parent@example.com"), http.StatusAccepted)
	if n := countRows(t, db, `SELECT COUNT(*) FROM password_reset_tokens`); n != 0 {
		t.Fatalf("token left behind after failed enqueue: %d", n)
	}
}

func TestEmailResetRejectsUnknownToken(t *testing.T) {
	db := testhelper.OpenDB(t)
	h := newResetHandlers(db, &fakeMailer{})
	expectStatus(t, completeReset(t, h, "not-a-real-token", newPassword), http.StatusBadRequest)
	expectStatus(t, completeReset(t, h, "", newPassword), http.StatusBadRequest)
}
