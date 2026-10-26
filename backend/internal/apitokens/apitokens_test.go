package apitokens

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

type fixture struct {
	db     *sql.DB
	clk    *clock.FakeClock
	h      *Handlers
	family testhelper.Family
}

func setup(t *testing.T) fixture {
	t.Helper()
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	clk := &clock.FakeClock{Time: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)}
	return fixture{db: conn, clk: clk, h: NewHandlers(conn, clk, nil), family: f}
}

func (f fixture) asMember(r *http.Request, memberID string) *http.Request {
	ctx := context.WithValue(r.Context(), permissions.ContextKeyFamilyMemberID, memberID)
	return r.WithContext(ctx)
}

// create issues a token through the handler and returns its raw value and id.
func (f fixture) create(t *testing.T, body string) (string, string) {
	t.Helper()
	req := f.asMember(httptest.NewRequest(http.MethodPost, "/api/v1/settings/api-tokens", strings.NewReader(body)), f.family.MemberID)
	rec := httptest.NewRecorder()
	f.h.Create(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("create: decode: %v", err)
	}
	return resp.Token, resp.ID
}

// guarded returns a handler wrapped by Require for scope that reports 200 when reached.
func (f fixture) guarded(scope string) http.HandlerFunc {
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	return Require(f.db, f.clk, scope)(ok)
}

func call(h http.HandlerFunc, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func (f fixture) denyPermission(t *testing.T, memberID, code string) {
	t.Helper()
	_, err := f.db.Exec(
		`INSERT INTO member_permission_overrides (id, family_member_id, permission_id, access_level)
		 SELECT ?, ?, id, 'denied' FROM permissions WHERE code = ?`,
		ids.New(), memberID, code,
	)
	if err != nil {
		t.Fatalf("deny %s: %v", code, err)
	}
}

func TestCreateShowsTokenOnceAndStoresOnlyHash(t *testing.T) {
	f := setup(t)
	raw, id := f.create(t, `{"name":"Home Assistant","scopes":["tasks:read"],"expires_in_days":365}`)
	if !strings.HasPrefix(raw, tokenPrefix) {
		t.Fatalf("token %q missing prefix", raw)
	}

	var stored string
	if err := f.db.QueryRow(`SELECT token_hash FROM api_tokens WHERE id = ?`, id).Scan(&stored); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if stored != auth.HashToken(raw) {
		t.Fatalf("stored hash does not match token")
	}
	if stored == raw {
		t.Fatalf("raw token stored in database")
	}

	var audits int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'api_token.created' AND target_id = ?`, id).Scan(&audits); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if audits != 1 {
		t.Fatalf("expected one created audit event, got %d", audits)
	}

	rec := httptest.NewRecorder()
	f.h.List(rec, f.asMember(httptest.NewRequest(http.MethodGet, "/api/v1/settings/api-tokens", nil), f.family.MemberID))
	if strings.Contains(rec.Body.String(), raw) {
		t.Fatalf("list response leaked the raw token")
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	f := setup(t)
	cases := map[string]string{
		"unknown scope": `{"name":"x","scopes":["admin:all"],"expires_in_days":30}`,
		"no scopes":     `{"name":"x","scopes":[],"expires_in_days":30}`,
		"bad expiry":    `{"name":"x","scopes":["tasks:read"],"expires_in_days":7}`,
		"empty name":    `{"name":"  ","scopes":["tasks:read"],"expires_in_days":30}`,
	}
	for name, body := range cases {
		req := f.asMember(httptest.NewRequest(http.MethodPost, "/api/v1/settings/api-tokens", strings.NewReader(body)), f.family.MemberID)
		rec := httptest.NewRecorder()
		f.h.Create(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

func TestCreateRejectsScopeOwnerLacks(t *testing.T) {
	f := setup(t)
	f.denyPermission(t, f.family.MemberID, permissions.PermTasksView)
	req := f.asMember(httptest.NewRequest(http.MethodPost, "/api/v1/settings/api-tokens",
		strings.NewReader(`{"name":"x","scopes":["tasks:read"],"expires_in_days":30}`)), f.family.MemberID)
	rec := httptest.NewRecorder()
	f.h.Create(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "scope_not_allowed") {
		t.Fatalf("status %d body %s, want 403 scope_not_allowed", rec.Code, rec.Body.String())
	}
}

func TestRequireAcceptsScopedTokenAndRecordsUse(t *testing.T) {
	f := setup(t)
	raw, id := f.create(t, `{"name":"x","scopes":["tasks:read"],"expires_in_days":30}`)

	if rec := call(f.guarded("tasks:read"), raw); rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", rec.Code, rec.Body.String())
	}
	var lastUsed sql.NullTime
	if err := f.db.QueryRow(`SELECT last_used_at FROM api_tokens WHERE id = ?`, id).Scan(&lastUsed); err != nil {
		t.Fatalf("read last_used_at: %v", err)
	}
	if !lastUsed.Valid {
		t.Fatalf("last_used_at not recorded")
	}
}

func TestRequireRejectsMissingAndUnknownTokens(t *testing.T) {
	f := setup(t)
	h := f.guarded("tasks:read")
	for name, token := range map[string]string{
		"missing":   "",
		"unknown":   tokenPrefix + "not-a-real-token",
		"no prefix": "abcdef",
	} {
		if rec := call(h, token); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}
	}
}

func TestRequireRejectsScopeNotGranted(t *testing.T) {
	f := setup(t)
	raw, _ := f.create(t, `{"name":"x","scopes":["calendar:read"],"expires_in_days":30}`)
	if rec := call(f.guarded("tasks:read"), raw); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}

func TestRequireRejectsRevokedToken(t *testing.T) {
	f := setup(t)
	raw, id := f.create(t, `{"name":"x","scopes":["tasks:read"],"expires_in_days":30}`)

	req := f.asMember(httptest.NewRequest(http.MethodPost, "/api/v1/settings/api-tokens/"+id+"/revoke", nil), f.family.MemberID)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	f.h.Revoke(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status %d, want 204", rec.Code)
	}

	if rec := call(f.guarded("tasks:read"), raw); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status %d, want 401", rec.Code)
	}
}

func TestRequireRejectsExpiredToken(t *testing.T) {
	f := setup(t)
	raw, _ := f.create(t, `{"name":"x","scopes":["tasks:read"],"expires_in_days":30}`)
	f.clk.Time = f.clk.Time.Add(31 * 24 * time.Hour)
	if rec := call(f.guarded("tasks:read"), raw); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired token status %d, want 401", rec.Code)
	}
}

func TestRequireRechecksOwnerPermission(t *testing.T) {
	f := setup(t)
	raw, _ := f.create(t, `{"name":"x","scopes":["tasks:read"],"expires_in_days":30}`)
	f.denyPermission(t, f.family.MemberID, permissions.PermTasksView)
	if rec := call(f.guarded("tasks:read"), raw); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d after owner lost access, want 403", rec.Code)
	}
}

func TestRevokeUnknownTokenIsNotFound(t *testing.T) {
	f := setup(t)
	req := f.asMember(httptest.NewRequest(http.MethodPost, "/api/v1/settings/api-tokens/missing/revoke", nil), f.family.MemberID)
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	f.h.Revoke(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}
