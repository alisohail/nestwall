package auth_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

const (
	currentPassword = "TestPassword1!"
	newPassword     = "NewPassword2!"
)

func newHandlers(db *sql.DB) *auth.Handlers {
	clk := clock.RealClock{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return auth.NewHandlers(db, auth.NewRateLimiter(clk), clk, logger)
}

// postJSON calls a handler with a JSON body. cookie is optional.
func postJSON(t *testing.T, fn http.HandlerFunc, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.10:4000"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, want, rec.Body.String())
	}
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// addChild inserts an active child user and member in the family.
func addChild(t *testing.T, db *sql.DB, familyID string) (userID string, memberID string) {
	t.Helper()
	ctx := context.Background()
	userUUID := ids.New()
	loginID := "kid" + userUUID[:12]
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, 'unused', 'Kid', 1)`,
		userUUID, loginID,
	); err != nil {
		t.Fatalf("insert child user: %v", err)
	}
	memberID = ids.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour)
		 VALUES (?, ?, ?, (SELECT id FROM roles WHERE name = 'Child'), 'active', '#F59E0B')`,
		memberID, familyID, userUUID,
	); err != nil {
		t.Fatalf("insert child member: %v", err)
	}
	return loginID, memberID
}

// addParent inserts a second active parent in the family.
func addParent(t *testing.T, db *sql.DB, familyID string) {
	t.Helper()
	ctx := context.Background()
	userUUID := ids.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, 'unused', 'Second Parent', 0)`,
		userUUID, "par"+userUUID[:12],
	); err != nil {
		t.Fatalf("insert parent user: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour)
		 VALUES (?, ?, ?, (SELECT id FROM roles WHERE name = 'Parent'), 'active', '#3B82F6')`,
		ids.New(), familyID, userUUID,
	); err != nil {
		t.Fatalf("insert parent member: %v", err)
	}
}

func TestMeReturnsOwnMemberID(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	h := newHandlers(db)

	sess, err := auth.CreateSession(context.Background(), db, clock.RealClock{}, fam.ParentUserID, "me-test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	var want string
	if err := db.QueryRow(`SELECT id FROM family_members WHERE user_id = ?`, fam.ParentUserID).Scan(&want); err != nil {
		t.Fatalf("parent member id: %v", err)
	}

	if _, err := db.Exec(`UPDATE families SET timezone = 'America/New_York' WHERE id = ?`, fam.FamilyID); err != nil {
		t.Fatalf("set family timezone: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.Token})
	rec := httptest.NewRecorder()
	h.Me(rec, req)
	expectStatus(t, rec, http.StatusOK)

	var body auth.MeResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Family == nil || body.Family.MemberID != want {
		t.Fatalf("family.member_id = %+v, want %q", body.Family, want)
	}
	if body.Family.Timezone != "America/New_York" {
		t.Fatalf("family.timezone = %q, want America/New_York", body.Family.Timezone)
	}
}

func TestForgotPasswordTellsAChildOnlyAParentCanReset(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	childLogin, _ := addChild(t, db, fam.FamilyID)
	h := newHandlers(db)

	child := postJSON(t, h.ForgotPassword, auth.ForgotPasswordRequest{UserID: childLogin}, nil)
	expectStatus(t, child, http.StatusAccepted)
	if !strings.Contains(child.Body.String(), `"status":"child"`) {
		t.Fatalf("child body = %s, want status child", child.Body.String())
	}
}

func TestForgotPasswordAnswersTheSameForAParentAndAnUnknownUser(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	h := newHandlers(db)

	known := postJSON(t, h.ForgotPassword, auth.ForgotPasswordRequest{UserID: fam.ParentUserIDTx}, nil)
	unknown := postJSON(t, h.ForgotPassword, auth.ForgotPasswordRequest{UserID: "no-such-user"}, nil)

	expectStatus(t, known, http.StatusAccepted)
	expectStatus(t, unknown, http.StatusAccepted)
	if known.Body.String() != unknown.Body.String() {
		t.Errorf("responses differ: %q vs %q", known.Body.String(), unknown.Body.String())
	}
	if !strings.Contains(known.Body.String(), `"status":"accepted"`) {
		t.Errorf("parent body = %s, want status accepted", known.Body.String())
	}
}

func TestForgotPasswordRejectsEmptyUserID(t *testing.T) {
	db := testhelper.OpenDB(t)
	h := newHandlers(db)
	expectStatus(t, postJSON(t, h.ForgotPassword, auth.ForgotPasswordRequest{UserID: "   "}, nil), http.StatusBadRequest)
}

func TestForgotPasswordByEmailRejectsInvalidAddress(t *testing.T) {
	db := testhelper.OpenDB(t)
	h := newHandlers(db)
	expectStatus(t, postJSON(t, h.ForgotPasswordByEmail, auth.ForgotPasswordEmailRequest{Email: "not-an-email"}, nil), http.StatusBadRequest)
	expectStatus(t, postJSON(t, h.ForgotPasswordByEmail, auth.ForgotPasswordEmailRequest{Email: ""}, nil), http.StatusBadRequest)
}

func TestForgotPasswordByEmailAnswersTheSameForAnyAddress(t *testing.T) {
	db := testhelper.OpenDB(t)
	h := newHandlers(db)

	known := postJSON(t, h.ForgotPasswordByEmail, auth.ForgotPasswordEmailRequest{Email: "sam@example.com"}, nil)
	unknown := postJSON(t, h.ForgotPasswordByEmail, auth.ForgotPasswordEmailRequest{Email: "nobody@example.com"}, nil)

	expectStatus(t, known, http.StatusAccepted)
	expectStatus(t, unknown, http.StatusAccepted)
	if known.Body.String() != unknown.Body.String() {
		t.Errorf("responses differ: %q vs %q", known.Body.String(), unknown.Body.String())
	}
}

func TestForgotPasswordIsRateLimitedPerAccount(t *testing.T) {
	db := testhelper.OpenDB(t)
	h := newHandlers(db)
	const perAccount = 5 // forgotMaxPerUser

	for i := 0; i < perAccount; i++ {
		expectStatus(t, postJSON(t, h.ForgotPassword, auth.ForgotPasswordRequest{UserID: "limited-user"}, nil), http.StatusAccepted)
	}
	expectStatus(t, postJSON(t, h.ForgotPassword, auth.ForgotPasswordRequest{UserID: "limited-user"}, nil), http.StatusTooManyRequests)

	// The limit is per account: another user ID from the same IP is still accepted.
	expectStatus(t, postJSON(t, h.ForgotPassword, auth.ForgotPasswordRequest{UserID: "other-user"}, nil), http.StatusAccepted)
}

func TestForgotPasswordIsRateLimitedPerIP(t *testing.T) {
	db := testhelper.OpenDB(t)
	h := newHandlers(db)
	const perIP = 20 // forgotMaxPerIP

	// Distinct user IDs keep each per-account count below its own limit.
	for i := 0; i < perIP; i++ {
		expectStatus(t, postJSON(t, h.ForgotPassword, auth.ForgotPasswordRequest{UserID: "ip-user-" + ids.New()[:8]}, nil), http.StatusAccepted)
	}
	expectStatus(t, postJSON(t, h.ForgotPassword, auth.ForgotPasswordRequest{UserID: "ip-user-" + ids.New()[:8]}, nil), http.StatusTooManyRequests)
}

func TestNotifyParentsCreatesOneNotificationPerParent(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	addParent(t, db, fam.FamilyID)
	childLogin, childMember := addChild(t, db, fam.FamilyID)
	h := newHandlers(db)

	expectStatus(t, postJSON(t, h.NotifyParents, auth.ForgotPasswordRequest{UserID: childLogin}, nil), http.StatusAccepted)

	const q = `SELECT COUNT(*) FROM notifications WHERE kind = 'password_reset_request' AND subject_member_id = ?`
	if n := countRows(t, db, q, childMember); n != 2 {
		t.Fatalf("notifications for child = %d, want 2 (one per parent)", n)
	}

	// An unknown user gets the same response and creates nothing.
	expectStatus(t, postJSON(t, h.NotifyParents, auth.ForgotPasswordRequest{UserID: "nobody"}, nil), http.StatusAccepted)
	if n := countRows(t, db, `SELECT COUNT(*) FROM notifications`); n != 2 {
		t.Errorf("total notifications = %d, want 2", n)
	}
}

func TestResetPasswordForcesChangeAndSignsOut(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	temp, err := auth.ResetPassword(ctx, tx, clock.RealClock{}, fam.ParentUserID)
	if err != nil {
		tx.Rollback()
		t.Fatalf("ResetPassword: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if n := countRows(t, db, `SELECT must_change_password FROM users WHERE id = ?`, fam.ParentUserID); n != 1 {
		t.Errorf("must_change_password = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM sessions WHERE user_id = ? AND revoked_at IS NULL`, fam.ParentUserID); n != 0 {
		t.Errorf("active sessions after reset = %d, want 0", n)
	}

	var hash string
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, fam.ParentUserID).Scan(&hash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if !auth.VerifyPassword(temp, hash) {
		t.Error("temporary password does not match the stored hash")
	}
	if auth.VerifyPassword(currentPassword, hash) {
		t.Error("old password still works after reset")
	}
}

func TestChangePasswordFlow(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	ctx := context.Background()
	h := newHandlers(db)

	other, err := auth.CreateSession(ctx, db, clock.RealClock{}, fam.ParentUserID, "other-device")
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}

	expectStatus(t, postJSON(t, h.ChangePassword, auth.ChangePasswordRequest{
		CurrentPassword: "not-the-password", NewPassword: newPassword,
	}, fam.SessionCookie), http.StatusUnauthorized)

	expectStatus(t, postJSON(t, h.ChangePassword, auth.ChangePasswordRequest{
		CurrentPassword: currentPassword, NewPassword: currentPassword,
	}, fam.SessionCookie), http.StatusBadRequest)

	expectStatus(t, postJSON(t, h.ChangePassword, auth.ChangePasswordRequest{
		CurrentPassword: currentPassword, NewPassword: newPassword,
	}, nil), http.StatusUnauthorized)

	expectStatus(t, postJSON(t, h.ChangePassword, auth.ChangePasswordRequest{
		CurrentPassword: currentPassword, NewPassword: newPassword,
	}, fam.SessionCookie), http.StatusNoContent)

	var hash string
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, fam.ParentUserID).Scan(&hash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if !auth.VerifyPassword(newPassword, hash) {
		t.Error("new password not stored")
	}
	if n := countRows(t, db, `SELECT must_change_password FROM users WHERE id = ?`, fam.ParentUserID); n != 0 {
		t.Errorf("must_change_password = %d, want 0", n)
	}
	if _, err := auth.ValidateSession(ctx, db, clock.RealClock{}, fam.SessionCookie.Value); err != nil {
		t.Errorf("current session should stay signed in: %v", err)
	}
	if _, err := auth.ValidateSession(ctx, db, clock.RealClock{}, other.Token); err == nil {
		t.Error("other session should be signed out")
	}
}
