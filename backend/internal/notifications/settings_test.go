package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/ids"
)

// send runs a handler as the signed-in parent with a JSON body.
func (f fixture) send(t *testing.T, method string, fn http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	ctx := context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, f.fam.MemberID)
	rec := httptest.NewRecorder()
	fn(rec, req.WithContext(ctx))
	return rec
}

// addChild adds an active child member to the family and returns the member ID.
func (f fixture) addChild(t *testing.T) string {
	t.Helper()
	userID, memberID := ids.New(), ids.New()
	if _, err := f.db.Exec(
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, 'x', 'Kid', 1)`,
		userID, "kid_"+userID,
	); err != nil {
		t.Fatalf("insert child user: %v", err)
	}
	if _, err := f.db.Exec(
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour)
		 SELECT ?, ?, ?, role_id, 'active', '#3B82F6' FROM family_members WHERE id = ?`,
		memberID, f.fam.FamilyID, userID, f.fam.MemberID,
	); err != nil {
		t.Fatalf("insert child member: %v", err)
	}
	return memberID
}

// enableSMTP marks the family mail server as set up.
func (f fixture) enableSMTP(t *testing.T) {
	t.Helper()
	if _, err := f.db.Exec(
		`INSERT INTO smtp_settings (family_id, enabled, host, from_address, updated_at) VALUES (?, 1, 'smtp.test', 'a@test', ?)`,
		f.fam.FamilyID, time.Now().UTC(),
	); err != nil {
		t.Fatalf("enable smtp: %v", err)
	}
}

const allOn = `{"in_app":true,"browser":true,"email":false,"email_digest":false,"digest_minutes":15,"email_template":""}`

func TestGetSettingsReturnsShippedDefaults(t *testing.T) {
	f := newFixture(t)
	rec := f.send(t, http.MethodGet, f.h.GetSettings, "")
	expectStatus(t, rec, http.StatusOK)

	var resp SettingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := notifypolicy.ShippedSettings()
	want.EmailTemplate = notifypolicy.DefaultTemplate
	if resp.Settings != want {
		t.Errorf("settings = %+v, want %+v", resp.Settings, want)
	}
	if resp.Settings.EmailDigest {
		t.Errorf("email_digest must default to false")
	}
	if resp.DefaultTemplate != notifypolicy.DefaultTemplate {
		t.Errorf("default_template is not the built-in template")
	}
}

func TestPutSettingsRoundTrips(t *testing.T) {
	f := newFixture(t)
	body := `{"in_app":true,"browser":false,"email":false,"email_digest":true,"digest_minutes":60,"email_template":""}`
	expectStatus(t, f.send(t, http.MethodPut, f.h.PutSettings, body), http.StatusOK)

	rec := f.send(t, http.MethodGet, f.h.GetSettings, "")
	expectStatus(t, rec, http.StatusOK)
	var resp SettingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Settings.Browser || !resp.Settings.EmailDigest {
		t.Errorf("saved switches not returned: %+v", resp.Settings)
	}
	if resp.Settings.DigestMinutes != 60 {
		t.Errorf("digest_minutes = %d, want 60", resp.Settings.DigestMinutes)
	}
}

func TestPutSettingsRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	cases := map[string]string{
		"missing switch":        `{"in_app":true,"browser":true,"email":false,"digest_minutes":15}`,
		"missing interval":      `{"in_app":true,"browser":true,"email":false,"email_digest":true,"email_template":""}`,
		"interval too small":    `{"in_app":true,"browser":true,"email":false,"email_digest":true,"digest_minutes":0,"email_template":""}`,
		"interval too large":    `{"in_app":true,"browser":true,"email":false,"email_digest":true,"digest_minutes":1441,"email_template":""}`,
		"template without body": `{"in_app":true,"browser":true,"email":false,"email_digest":true,"digest_minutes":15,"email_template":"<p>hi</p>"}`,
		"unknown field":         `{"in_app":true,"browser":true,"email":false,"email_digest":true,"digest_minutes":15,"email_template":"","x":1}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, f.send(t, http.MethodPut, f.h.PutSettings, body), http.StatusBadRequest)
		})
	}
}

func TestEmailNeedsMailServer(t *testing.T) {
	f := newFixture(t)
	body := `{"in_app":true,"browser":true,"email":true,"email_digest":true,"digest_minutes":15,"email_template":""}`
	expectStatus(t, f.send(t, http.MethodPut, f.h.PutSettings, body), http.StatusUnprocessableEntity)

	f.enableSMTP(t)
	expectStatus(t, f.send(t, http.MethodPut, f.h.PutSettings, body), http.StatusOK)
}

func TestNotifyMarksParentEmailOnlyWhenEnabled(t *testing.T) {
	f := newFixture(t)
	f.enableSMTP(t)
	child := f.addChild(t)
	ctx := context.Background()
	now := time.Now().UTC()

	on := notifypolicy.ShippedSettings()
	on.Email = true
	if err := notifypolicy.Save(ctx, f.db, f.fam.FamilyID, on, now); err != nil {
		t.Fatalf("save: %v", err)
	}
	for _, rcpt := range []string{f.fam.MemberID, child} {
		if _, err := notifypolicy.Notify(ctx, f.db, notifypolicy.Notice{
			FamilyID: f.fam.FamilyID, RecipientID: rcpt, Kind: "task_approved", Body: "Done",
		}, now); err != nil {
			t.Fatalf("notify: %v", err)
		}
	}

	var parentPending, childPending bool
	f.db.QueryRow(`SELECT email_pending FROM notifications WHERE recipient_id = ?`, f.fam.MemberID).Scan(&parentPending)
	f.db.QueryRow(`SELECT email_pending FROM notifications WHERE recipient_id = ?`, child).Scan(&childPending)
	if !parentPending {
		t.Errorf("parent notice not marked for email")
	}
	if childPending {
		t.Errorf("child notice must never be marked for email")
	}
}

func TestNotifyWritesNothingWhenInAppOff(t *testing.T) {
	f := newFixture(t)
	off := notifypolicy.ShippedSettings()
	off.InApp = false
	if err := notifypolicy.Save(context.Background(), f.db, f.fam.FamilyID, off, time.Now().UTC()); err != nil {
		t.Fatalf("save: %v", err)
	}
	ok, err := notifypolicy.Notify(context.Background(), f.db, notifypolicy.Notice{
		FamilyID: f.fam.FamilyID, RecipientID: f.fam.MemberID, Kind: "task_approved", Body: "Done",
	}, time.Now().UTC())
	if err != nil || ok {
		t.Fatalf("Notify = %v, %v; want false, nil", ok, err)
	}
	var n int
	f.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE recipient_id = ?`, f.fam.MemberID).Scan(&n)
	if n != 0 {
		t.Errorf("notifications written = %d, want 0", n)
	}
}

func TestPreviewRendersFamilyAndBody(t *testing.T) {
	f := newFixture(t)
	body := `{"email_template":"<p>{{family_name}}</p><img src=\"{{logo_url}}\">{{body}}"}`
	rec := f.send(t, http.MethodPost, f.h.PreviewTemplate, body)
	expectStatus(t, rec, http.StatusOK)

	var resp PreviewResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(resp.HTML, "Test Family") || !strings.Contains(resp.HTML, "Tidy the kitchen") {
		t.Errorf("preview missing family or sample body: %s", resp.HTML)
	}
	// The browser cannot load cid: images, so the preview inlines the logo as a data URI.
	if strings.Contains(resp.HTML, "cid:") || !strings.Contains(resp.HTML, "data:image/png;base64,") {
		t.Errorf("preview logo not inlined: %s", resp.HTML)
	}
}

func TestClearAllRemovesOnlyCallersNotifications(t *testing.T) {
	f := newFixture(t)
	f.insert(t, time.Now().UTC(), nil)
	f.insert(t, time.Now().UTC(), nil)

	expectStatus(t, f.send(t, http.MethodDelete, f.h.ClearAll, ""), http.StatusNoContent)

	var n int
	f.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE recipient_id = ?`, f.fam.MemberID).Scan(&n)
	if n != 0 {
		t.Errorf("remaining notifications = %d, want 0", n)
	}
}
