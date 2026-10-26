package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/email"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/config"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

// childRoleID is the seeded Child role from migration 000001.
const childRoleID = "c3d4e5f6-a7b8-9012-cdef-123456789012"

// isolationClient sends authenticated requests through the full server handler
// chain (request ID, logging, CSRF, routes, permission middleware).
type isolationClient struct {
	t       *testing.T
	h       http.Handler
	session *http.Cookie
	csrf    string
}

func newIsolationClient(t *testing.T, h http.Handler, session *http.Cookie) *isolationClient {
	t.Helper()
	c := &isolationClient{t: t, h: h, session: session}

	// A GET without a CSRF cookie makes the server issue a token in the response header.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	c.csrf = rec.Header().Get(auth.CSRFHeaderName)
	if c.csrf == "" {
		t.Fatal("no CSRF token issued")
	}
	return c
}

func (c *isolationClient) do(method, path, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	if c.session != nil {
		req.AddCookie(c.session)
	}
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: c.csrf})
	req.Header.Set(auth.CSRFHeaderName, c.csrf)

	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

// addChild inserts an active child member with a session into the family.
func addChild(t *testing.T, conn *sql.DB, familyID, name string) *http.Cookie {
	t.Helper()
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	userID := ids.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, 'unused', ?, 1)`,
		userID, "child_"+userID[:8], name,
	); err != nil {
		t.Fatalf("insert child user: %v", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour) VALUES (?, ?, ?, ?, 'active', '#F59E0B')`,
		ids.New(), familyID, userID, childRoleID,
	); err != nil {
		t.Fatalf("insert child member: %v", err)
	}
	sess, err := auth.CreateSession(ctx, tx, clock.RealClock{}, userID, "isolation-test")
	if err != nil {
		t.Fatalf("create child session: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit child: %v", err)
	}
	return &http.Cookie{Name: auth.SessionCookieName, Value: sess.Token}
}

func newIsolationServer(conn *sql.DB) http.Handler {
	cfg := &config.Config{}
	cfg.API.Port = "0"
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, log, clock.RealClock{}, conn, email.NewOutbox(conn, clock.RealClock{}, log, "")).httpServer.Handler
}

// TestChildIsolation checks that a child account cannot manage members or read the
// audit log, and can still read its own profile.
func TestChildIsolation(t *testing.T) {
	conn := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, conn)
	childCookie := addChild(t, conn, fam.FamilyID, "Ava")
	h := newIsolationServer(conn)
	child := newIsolationClient(t, h, childCookie)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list members", http.MethodGet, "/api/v1/family/members", ""},
		{"update parent", http.MethodPut, "/api/v1/family/members/" + fam.MemberID, `{"colour":"#000000"}`},
		{"remove parent", http.MethodDelete, "/api/v1/family/members/" + fam.MemberID, ""},
		{"create member", http.MethodPost, "/api/v1/family/members", `{}`},
		{"rename family", http.MethodPut, "/api/v1/family", `{"family_name":"Hacked"}`},
		{"read audit log", http.MethodGet, "/api/v1/audit/logs", ""},
		{"create task", http.MethodPost, "/api/v1/tasks", `{}`},
		{"update task", http.MethodPatch, "/api/v1/tasks/" + fam.MemberID, `{"title":"x"}`},
		{"archive task", http.MethodPost, "/api/v1/tasks/" + fam.MemberID + "/archive", ""},
		{"create reward", http.MethodPost, "/api/v1/rewards", `{"name":"Treat","cost":10}`},
		{"update reward", http.MethodPatch, "/api/v1/rewards/" + fam.MemberID, `{"cost":2}`},
		{"archive reward", http.MethodDelete, "/api/v1/rewards/" + fam.MemberID, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := child.do(tt.method, tt.path, tt.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("child can still read own profile", func(t *testing.T) {
		rec := child.do(http.MethodGet, "/api/v1/auth/me", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200", rec.Code)
		}
	})
}

// wallDeviceCookieName is the wall device credential cookie set by the wall package.
const wallDeviceCookieName = "nestwall-wall-device"

// enrolWallDevice creates a device as a parent, pairs it through the server routes and
// returns the device ID and its credential cookie.
func enrolWallDevice(t *testing.T, h http.Handler, parent *isolationClient) (string, *http.Cookie) {
	t.Helper()
	rec := parent.do(http.MethodPost, "/api/v1/wall/devices", `{"name":"Hall","exit_code":"1234"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create device: status %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID         string `json:"id"`
		PairingKey string `json:"pairing_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create device: %v", err)
	}

	// Pairing is anonymous: it sends the CSRF token but no session cookie.
	anon := &isolationClient{t: t, h: h, csrf: parent.csrf}
	rec = anon.do(http.MethodPost, "/api/v1/wall/pair", fmt.Sprintf(`{"key":%q}`, created.PairingKey))
	if rec.Code != http.StatusOK {
		t.Fatalf("pair: status %d: %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == wallDeviceCookieName {
			return created.ID, &http.Cookie{Name: c.Name, Value: c.Value}
		}
	}
	t.Fatal("pair did not set the device cookie")
	return "", nil
}

// TestWallDeviceIsLimitedToWallEndpoints checks that a paired wall device can read only the
// wall endpoints. Session-only routes refuse its credential through the full server chain.
func TestWallDeviceIsLimitedToWallEndpoints(t *testing.T) {
	conn := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, conn)
	h := newIsolationServer(conn)
	parent := newIsolationClient(t, h, fam.SessionCookie)
	_, cookie := enrolWallDevice(t, h, parent)
	device := &isolationClient{t: t, h: h, session: cookie, csrf: parent.csrf}

	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/auth/me", ""},
		{http.MethodGet, "/api/v1/family/members", ""},
		{http.MethodGet, "/api/v1/audit/logs", ""},
		{http.MethodGet, "/api/v1/tasks", ""},
		{http.MethodPost, "/api/v1/tasks", `{}`},
		{http.MethodGet, "/api/v1/rewards", ""},
		{http.MethodPost, "/api/v1/wall/devices", `{"name":"Porch","exit_code":"1234"}`},
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			rec := device.do(r.method, r.path, r.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401: %s", rec.Code, rec.Body.String())
			}
		})
	}

	for _, path := range []string{"/api/v1/wall/device", "/api/v1/wall/summary"} {
		t.Run("wall reads "+path, func(t *testing.T) {
			rec := device.do(http.MethodGet, path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestRevokedWallDeviceLosesWallAccess checks that revoking a device ends its wall access
// on the next request, through the full server chain.
func TestRevokedWallDeviceLosesWallAccess(t *testing.T) {
	conn := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, conn)
	h := newIsolationServer(conn)
	parent := newIsolationClient(t, h, fam.SessionCookie)
	deviceID, cookie := enrolWallDevice(t, h, parent)
	device := &isolationClient{t: t, h: h, session: cookie, csrf: parent.csrf}

	if rec := device.do(http.MethodGet, "/api/v1/wall/device", ""); rec.Code != http.StatusOK {
		t.Fatalf("before revoke: status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	if rec := parent.do(http.MethodPost, "/api/v1/wall/devices/"+deviceID+"/revoke", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status %d, want 204: %s", rec.Code, rec.Body.String())
	}

	if rec := device.do(http.MethodGet, "/api/v1/wall/device", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after revoke: status %d, want 401: %s", rec.Code, rec.Body.String())
	}
}
