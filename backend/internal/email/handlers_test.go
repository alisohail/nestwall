package email

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/testhelper"
)

const testDBKey = "email-handler-test-key"

type handlerFixture struct {
	h   *Handlers
	db  *sql.DB
	fam testhelper.Family
}

func newHandlerFixture(t *testing.T) handlerFixture {
	t.Helper()
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	if _, err := db.Exec(`UPDATE users SET email = ? WHERE id = ?`, "parent@example.com", fam.ParentUserID); err != nil {
		t.Fatalf("set user email: %v", err)
	}
	h := NewHandlers(db, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), testDBKey)
	return handlerFixture{h: h, db: db, fam: fam}
}

func (f handlerFixture) call(t *testing.T, fn http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/settings/email", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, f.fam.MemberID)
	ctx = context.WithValue(ctx, permissions.ContextKeyUserID, f.fam.ParentUserID)
	rec := httptest.NewRecorder()
	fn(rec, req.WithContext(ctx))
	return rec
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

func expectErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("error body %s does not mention %q", rec.Body.String(), want)
	}
}

const validSettings = `{"host":"smtp.example.com","port":587,"security":"starttls",` +
	`"username":"mailer","password":"s3cret","from_address":"nestwall@example.com","from_name":"NestWall","send_limit_per_hour":30}`

func storedSealed(t *testing.T, f handlerFixture) string {
	t.Helper()
	var sealed string
	if err := f.db.QueryRow(`SELECT password_sealed FROM smtp_settings WHERE family_id = ?`, f.fam.FamilyID).Scan(&sealed); err != nil {
		t.Fatalf("read sealed password: %v", err)
	}
	return sealed
}

func TestGetReturnsDefaultsWhenNothingSaved(t *testing.T) {
	f := newHandlerFixture(t)
	rec := f.call(t, f.h.GetSettings, http.MethodGet, "")
	expectStatus(t, rec, http.StatusOK)
	var got Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Ready || got.PasswordSet || got.Port != 587 || got.Security != securitySTARTTLS || got.SendLimitPerHour != 30 {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestUpdateSealsPasswordAndNeverReturnsIt(t *testing.T) {
	f := newHandlerFixture(t)
	rec := f.call(t, f.h.UpdateSettings, http.MethodPut, validSettings)
	expectStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("response leaks the password: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"password_set":true`) {
		t.Fatalf("password_set not reported: %s", rec.Body.String())
	}

	sealed := storedSealed(t, f)
	if sealed == "" || strings.Contains(sealed, "s3cret") {
		t.Fatalf("password not sealed: %q", sealed)
	}
	plain, err := openSecret(testDBKey, f.fam.FamilyID, sealed)
	if err != nil || plain != "s3cret" {
		t.Fatalf("sealed password does not round-trip: %q, %v", plain, err)
	}

	get := f.call(t, f.h.GetSettings, http.MethodGet, "")
	expectStatus(t, get, http.StatusOK)
	if strings.Contains(get.Body.String(), "s3cret") || !strings.Contains(get.Body.String(), `"password_set":true`) {
		t.Fatalf("GET: %s", get.Body.String())
	}
}

func TestUpdateKeepsPasswordWhenOmittedAndClearsWhenEmpty(t *testing.T) {
	f := newHandlerFixture(t)
	expectStatus(t, f.call(t, f.h.UpdateSettings, http.MethodPut, validSettings), http.StatusOK)
	before := storedSealed(t, f)

	omitted := `{"host":"smtp.example.com","port":2525,"security":"starttls","username":"mailer","from_address":"nestwall@example.com"}`
	expectStatus(t, f.call(t, f.h.UpdateSettings, http.MethodPut, omitted), http.StatusOK)
	if got := storedSealed(t, f); got != before {
		t.Fatal("omitting the password changed the stored secret")
	}

	cleared := `{"host":"smtp.example.com","port":2525,"security":"starttls","username":"mailer","password":"","from_address":"nestwall@example.com"}`
	expectStatus(t, f.call(t, f.h.UpdateSettings, http.MethodPut, cleared), http.StatusOK)
	if got := storedSealed(t, f); got != "" {
		t.Fatal("empty password did not clear the stored secret")
	}
}

func TestUpdateRejectsInvalidSettings(t *testing.T) {
	cases := map[string]string{
		"port out of range":    `{"host":"smtp.example.com","port":70000,"security":"starttls","from_address":"nestwall@example.com"}`,
		"unknown security":     `{"host":"smtp.example.com","port":587,"security":"none","from_address":"nestwall@example.com"}`,
		"bad sender address":   `{"host":"smtp.example.com","port":587,"security":"starttls","from_address":"not an address"}`,
		"host with scheme":     `{"host":"smtps://smtp.example.com","port":587,"security":"starttls","from_address":"nestwall@example.com"}`,
		"send limit too large": `{"host":"smtp.example.com","port":587,"security":"starttls","from_address":"nestwall@example.com","send_limit_per_hour":5000}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			f := newHandlerFixture(t)
			rec := f.call(t, f.h.UpdateSettings, http.MethodPut, body)
			expectStatus(t, rec, http.StatusBadRequest)
			expectErrorCode(t, rec, "validation_failed")
		})
	}
}

func TestSaveWithoutSenderIsSavedButNotReady(t *testing.T) {
	f := newHandlerFixture(t)
	rec := f.call(t, f.h.UpdateSettings, http.MethodPut, `{"host":"smtp.example.com","port":587,"security":"starttls"}`)
	expectStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"ready":false`) {
		t.Fatalf("partial settings should not be ready: %s", rec.Body.String())
	}
}

func TestSendTestRequiresReadySettings(t *testing.T) {
	f := newHandlerFixture(t)
	rec := f.call(t, f.h.SendTest, http.MethodPost, "")
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	expectErrorCode(t, rec, "smtp_not_ready")
}

func TestSendTestRequiresAccountEmail(t *testing.T) {
	f := newHandlerFixture(t)
	expectStatus(t, f.call(t, f.h.UpdateSettings, http.MethodPut,
		`{"host":"127.0.0.1","port":587,"security":"starttls","from_address":"nestwall@example.com"}`), http.StatusOK)
	if _, err := f.db.Exec(`UPDATE users SET email = NULL WHERE id = ?`, f.fam.ParentUserID); err != nil {
		t.Fatalf("clear email: %v", err)
	}
	rec := f.call(t, f.h.SendTest, http.MethodPost, "")
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	expectErrorCode(t, rec, "no_email")
}

func TestSendTestDeliversToSignedInUser(t *testing.T) {
	f := newHandlerFixture(t)
	srv, clientCfg := newFakeSMTP(t, true, false, "mailer", "s3cret")
	f.h.Mailer = Mailer{TLSConfig: clientCfg}
	body := `{"host":"127.0.0.1","port":` + itoa(srv.port()) + `,"security":"tls","username":"mailer",` +
		`"password":"s3cret","from_address":"nestwall@example.com"}`
	expectStatus(t, f.call(t, f.h.UpdateSettings, http.MethodPut, body), http.StatusOK)

	rec := f.call(t, f.h.SendTest, http.MethodPost, "")
	expectStatus(t, rec, http.StatusNoContent)
	_, ok, _, rcpt, data := srv.received()
	if !ok || !strings.Contains(rcpt, "parent@example.com") || !strings.Contains(data, "NestWall test email") {
		t.Fatalf("message not delivered as expected: ok=%v rcpt=%q", ok, rcpt)
	}
}

func TestSendTestReportsServerFailureWithoutLeakingPassword(t *testing.T) {
	f := newHandlerFixture(t)
	srv, clientCfg := newFakeSMTP(t, true, false, "mailer", "right-pass")
	f.h.Mailer = Mailer{TLSConfig: clientCfg}
	body := `{"host":"127.0.0.1","port":` + itoa(srv.port()) + `,"security":"tls","username":"mailer",` +
		`"password":"wrong-s3cret","from_address":"nestwall@example.com"}`
	expectStatus(t, f.call(t, f.h.UpdateSettings, http.MethodPut, body), http.StatusOK)

	rec := f.call(t, f.h.SendTest, http.MethodPost, "")
	expectStatus(t, rec, http.StatusBadGateway)
	expectErrorCode(t, rec, "send_failed")
	if strings.Contains(rec.Body.String(), "wrong-s3cret") {
		t.Fatalf("response leaks the password: %s", rec.Body.String())
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
