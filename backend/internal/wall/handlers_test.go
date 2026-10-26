package wall

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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
	"nestwall/backend/internal/weather"
)

// childRoleID is the seeded Child role from migration 000001.
const childRoleID = "c3d4e5f6-a7b8-9012-cdef-123456789012"

const (
	parentRemote = "192.0.2.10:5000"
	unknownKey   = "ABCD-EFGH"
)

// testClock is read by request and stream goroutines, so tests move it with advance.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type wallFixture struct {
	db        *sql.DB
	mux       *http.ServeMux
	clk       *testClock
	parent    *http.Cookie
	kid       *http.Cookie
	upstream  *weatherStub
	avatarDir string
}

// weatherStub stands in for Open-Meteo. It answers with a fixed forecast, or fails when fail is set.
type weatherStub struct {
	url  string
	fail atomic.Bool
}

func newWeatherStub(t *testing.T) *weatherStub {
	t.Helper()
	s := &weatherStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.fail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"current":{"temperature_2m":18.4,"apparent_temperature":17.1,"weather_code":3},`+
			`"daily":{"weather_code":[61,2],"temperature_2m_max":[21.5,22],"temperature_2m_min":[12.2,11],`+
			`"precipitation_probability_max":[70,null]}}`)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func newWallFixture(t *testing.T) wallFixture {
	t.Helper()
	conn := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, conn)
	clk := &testClock{now: time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	upstream := newWeatherStub(t)
	wx := weather.NewService(conn, weather.NewClient(upstream.url, clk), logger)
	avatarDir := t.TempDir()
	h := NewHandlers(conn, auth.NewRateLimiter(clk), clk, logger, wx, avatarDir)

	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/wall/devices", permissions.RequireParent(conn, clk)(h.CreateDevice))
	mux.Handle("GET /api/v1/wall/devices", permissions.RequireParent(conn, clk)(h.ListDevices))
	mux.Handle("PATCH /api/v1/wall/devices/{id}", permissions.RequireParent(conn, clk)(h.UpdateDevice))
	mux.HandleFunc("POST /api/v1/wall/pair", h.Pair)
	mux.HandleFunc("GET /api/v1/wall/device", h.RequireDevice(h.GetDevice))
	mux.HandleFunc("GET /api/v1/wall/summary", h.RequireDevice(h.Summary))
	mux.HandleFunc("GET /api/v1/wall/events", h.RequireDevice(h.Events))
	mux.HandleFunc("GET /api/v1/wall/avatars/{id}", h.RequireDevice(h.ServeAvatar))
	mux.HandleFunc("POST /api/v1/wall/exit/verify", h.RequireDevice(h.VerifyExitCode))
	mux.HandleFunc("POST /api/v1/wall/unpair", h.RequireDevice(h.UnpairDevice))
	mux.Handle("POST /api/v1/wall/devices/{id}/revoke", permissions.RequireParent(conn, clk)(h.RevokeDevice))

	return wallFixture{
		db:        conn,
		mux:       mux,
		clk:       clk,
		upstream:  upstream,
		avatarDir: avatarDir,
		parent:    fam.SessionCookie,
		kid:       addChild(t, conn, clk, fam.FamilyID),
	}
}

// addChild inserts an active child member with the Child role and a valid session.
func addChild(t *testing.T, conn *sql.DB, clk *testClock, familyID string) *http.Cookie {
	t.Helper()
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin child: %v", err)
	}
	defer tx.Rollback()

	userID, memberID := ids.New(), ids.New()
	hash, err := auth.HashPassword("TestPassword1!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, ?, 'Kid', 1)`,
		userID, "kid_"+userID[:8], hash); err != nil {
		t.Fatalf("insert child user: %v", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour) VALUES (?, ?, ?, ?, 'active', '#F59E0B')`,
		memberID, familyID, userID, childRoleID); err != nil {
		t.Fatalf("insert child member: %v", err)
	}
	sess, err := auth.CreateSession(ctx, tx, clk, userID, "wall-test")
	if err != nil {
		t.Fatalf("create child session: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit child: %v", err)
	}
	return &http.Cookie{Name: auth.SessionCookieName, Value: sess.Token}
}

// send issues a request through the mux. A nil cookie sends the request anonymously.
func send(mux *http.ServeMux, method, path, body string, cookie *http.Cookie, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remote
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func createDevice(t *testing.T, f wallFixture, name, exitCode string) createDeviceResponse {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"exit_code":%q}`, name, exitCode)
	rec := send(f.mux, http.MethodPost, "/api/v1/wall/devices", body, f.parent, parentRemote)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create device: status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp createDeviceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return resp
}

func pair(f wallFixture, key, remote string) *httptest.ResponseRecorder {
	return send(f.mux, http.MethodPost, "/api/v1/wall/pair", fmt.Sprintf(`{"key":%q}`, key), nil, remote)
}

func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestParentCreatesDeviceAndStoresOnlyHashes(t *testing.T) {
	f := newWallFixture(t)

	resp := createDevice(t, f, "Living room", "1234")

	if len(resp.PairingKey) != 9 || resp.PairingKey[4] != '-' {
		t.Fatalf("pairing key %q is not XXXX-XXXX", resp.PairingKey)
	}
	if !validKey(normalizeKey(resp.PairingKey)) {
		t.Fatalf("pairing key %q has symbols outside the alphabet", resp.PairingKey)
	}
	if want := f.clk.now.Add(pairingKeyTTL); !resp.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %v, want %v", resp.ExpiresAt, want)
	}

	var codeHash, exitHash string
	if err := f.db.QueryRow(`SELECT code_hash FROM wall_pairing_codes WHERE device_id = ?`, resp.ID).Scan(&codeHash); err != nil {
		t.Fatalf("read code hash: %v", err)
	}
	if codeHash != auth.HashToken(normalizeKey(resp.PairingKey)) {
		t.Fatal("stored code_hash does not match the key")
	}
	if codeHash == resp.PairingKey {
		t.Fatal("pairing key stored in plaintext")
	}

	if err := f.db.QueryRow(`SELECT exit_code_hash FROM wall_devices WHERE id = ?`, resp.ID).Scan(&exitHash); err != nil {
		t.Fatalf("read exit code hash: %v", err)
	}
	if !auth.VerifyPassword("1234", exitHash) {
		t.Fatal("exit code hash does not verify against the chosen exit code")
	}
	if auth.VerifyPassword("4321", exitHash) {
		t.Fatal("exit code hash verifies a different exit code")
	}
}

func TestCreateDeviceRejectsInvalidInput(t *testing.T) {
	f := newWallFixture(t)
	cases := []struct {
		name     string
		exitCode string
	}{
		{"", "1234"},
		{"   ", "1234"},
		{strings.Repeat("x", maxNameRunes+1), "1234"},
		{"Hall", "123"},
		{"Hall", "1234567"},
		{"Hall", "12a4"},
	}
	for _, tc := range cases {
		body := fmt.Sprintf(`{"name":%q,"exit_code":%q}`, tc.name, tc.exitCode)
		rec := send(f.mux, http.MethodPost, "/api/v1/wall/devices", body, f.parent, parentRemote)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "validation_failed") {
			t.Errorf("name=%q exit=%q: status %d, body %s; want 400 validation_failed",
				tc.name, tc.exitCode, rec.Code, rec.Body.String())
		}
	}
}

func TestChildCannotCreateDevice(t *testing.T) {
	f := newWallFixture(t)
	body := `{"name":"Hall","exit_code":"1234"}`
	rec := send(f.mux, http.MethodPost, "/api/v1/wall/devices", body, f.kid, parentRemote)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("child create: status %d, want 403", rec.Code)
	}
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM wall_devices`).Scan(&n); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if n != 0 {
		t.Fatalf("child create left %d device rows", n)
	}
}

func TestPairingSucceedsOnceAndSetsDeviceCookie(t *testing.T) {
	f := newWallFixture(t)
	dev := createDevice(t, f, "Kitchen", "1234")

	// Typed input: lowercase, no dash. It must still pair.
	typed := strings.ToLower(strings.ReplaceAll(dev.PairingKey, "-", ""))
	rec := pair(f, typed, "203.0.113.1:4000")
	if rec.Code != http.StatusOK {
		t.Fatalf("pair: status %d, body %s", rec.Code, rec.Body.String())
	}
	var got pairResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode pair response: %v", err)
	}
	if got.ID != dev.ID || got.Name != "Kitchen" {
		t.Fatalf("pair response = %+v, want device %s Kitchen", got, dev.ID)
	}

	cookie := findCookie(rec, deviceCookie)
	if cookie == nil {
		t.Fatal("pair did not set the device cookie")
	}
	if cookie.Path != "/api/v1/wall" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge <= 0 {
		t.Fatalf("device cookie flags wrong: path=%q httpOnly=%v sameSite=%v maxAge=%d",
			cookie.Path, cookie.HttpOnly, cookie.SameSite, cookie.MaxAge)
	}

	var credentialHash string
	var pairedAt, usedAt sql.NullTime
	if err := f.db.QueryRow(`SELECT credential_hash, paired_at FROM wall_devices WHERE id = ?`, dev.ID).
		Scan(&credentialHash, &pairedAt); err != nil {
		t.Fatalf("read device: %v", err)
	}
	if credentialHash != auth.HashToken(cookie.Value) {
		t.Fatal("stored credential_hash does not match the cookie value")
	}
	if credentialHash == cookie.Value {
		t.Fatal("device credential stored in plaintext")
	}
	if !pairedAt.Valid {
		t.Fatal("paired_at not set")
	}
	if err := f.db.QueryRow(`SELECT used_at FROM wall_pairing_codes WHERE device_id = ?`, dev.ID).Scan(&usedAt); err != nil {
		t.Fatalf("read pairing code: %v", err)
	}
	if !usedAt.Valid {
		t.Fatal("used_at not set after pairing")
	}

	again := pair(f, dev.PairingKey, "203.0.113.1:4000")
	if again.Code != http.StatusBadRequest || !strings.Contains(again.Body.String(), "pairing_key_invalid") {
		t.Fatalf("reused key: status %d, body %s; want 400 pairing_key_invalid", again.Code, again.Body.String())
	}
	if findCookie(again, deviceCookie) != nil {
		t.Fatal("reused key set a device cookie")
	}
}

func TestPairingRejectsUnusableKeys(t *testing.T) {
	t.Run("unknown key", func(t *testing.T) {
		f := newWallFixture(t)
		createDevice(t, f, "Hall", "1234")
		rec := pair(f, unknownKey, "203.0.113.2:4000")
		assertRejected(t, rec)
	})

	t.Run("malformed key", func(t *testing.T) {
		f := newWallFixture(t)
		rec := pair(f, "not a key", "203.0.113.2:4000")
		assertRejected(t, rec)
	})

	t.Run("expired after ten minutes", func(t *testing.T) {
		f := newWallFixture(t)
		dev := createDevice(t, f, "Hall", "1234")
		f.clk.now = f.clk.now.Add(pairingKeyTTL)
		rec := pair(f, dev.PairingKey, "203.0.113.2:4000")
		assertRejected(t, rec)
		assertKeyUnused(t, f, dev.ID)
	})

	t.Run("revoked device", func(t *testing.T) {
		f := newWallFixture(t)
		dev := createDevice(t, f, "Hall", "1234")
		mustExec(t, f.db, `UPDATE wall_devices SET revoked_at = ? WHERE id = ?`, f.clk.now, dev.ID)
		rec := pair(f, dev.PairingKey, "203.0.113.2:4000")
		assertRejected(t, rec)
		assertKeyUnused(t, f, dev.ID)
	})
}

func assertRejected(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "pairing_key_invalid") {
		t.Fatalf("status %d, body %s; want 400 pairing_key_invalid", rec.Code, rec.Body.String())
	}
	if findCookie(rec, deviceCookie) != nil {
		t.Fatal("rejected key set a device cookie")
	}
}

func assertKeyUnused(t *testing.T, f wallFixture, deviceID string) {
	t.Helper()
	var usedAt sql.NullTime
	if err := f.db.QueryRow(`SELECT used_at FROM wall_pairing_codes WHERE device_id = ?`, deviceID).Scan(&usedAt); err != nil {
		t.Fatalf("read pairing code: %v", err)
	}
	if usedAt.Valid {
		t.Fatal("a rejected attempt consumed the key")
	}
}

func mustExec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func TestPairingIsRateLimitedPerIP(t *testing.T) {
	f := newWallFixture(t)
	dev := createDevice(t, f, "Hall", "1234")

	const attacker = "198.51.100.7:4000"
	for i := 0; i < pairMaxAttempts; i++ {
		assertRejected(t, pair(f, unknownKey, attacker))
	}
	rec := pair(f, dev.PairingKey, attacker)
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("attempt %d: status %d, body %s; want 429 rate_limited",
			pairMaxAttempts+1, rec.Code, rec.Body.String())
	}

	// The limit is per IP, so a tablet on another address still pairs.
	if rec := pair(f, dev.PairingKey, "203.0.113.50:4000"); rec.Code != http.StatusOK {
		t.Fatalf("other IP: status %d, body %s; want 200", rec.Code, rec.Body.String())
	}

	// Once the window has passed, the blocked IP can pair again.
	f.clk.now = f.clk.now.Add(pairWindow + time.Second)
	later := createDevice(t, f, "Porch", "5678")
	if rec := pair(f, later.PairingKey, attacker); rec.Code != http.StatusOK {
		t.Fatalf("after window: status %d, body %s; want 200", rec.Code, rec.Body.String())
	}
}
