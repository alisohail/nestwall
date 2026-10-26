package integrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/apitokens"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/testhelper"
)

type fixture struct {
	db     *sql.DB
	clk    *clock.FakeClock
	family testhelper.Family
	tokens *apitokens.Handlers
	h      *Handlers
}

func setup(t *testing.T) fixture {
	t.Helper()
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	clk := &clock.FakeClock{Time: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)}
	return fixture{db: conn, clk: clk, family: f, tokens: apitokens.NewHandlers(conn, clk, nil), h: NewHandlers(conn, clk, nil)}
}

// createToken issues a token for the parent through the token handler and returns the raw value.
func (f fixture) createToken(t *testing.T, scopes string) string {
	t.Helper()
	body := `{"name":"Home Assistant","scopes":[` + scopes + `],"expires_in_days":30}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/api-tokens", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, f.family.MemberID))
	rec := httptest.NewRecorder()
	f.tokens.Create(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token: status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	return resp.Token
}

// summaryRoute mirrors the wiring in server.go: one Require wrap per scope, outermost first.
func (f fixture) summaryRoute(scopes ...string) http.HandlerFunc {
	var h http.HandlerFunc = f.h.Summary
	for i := len(scopes) - 1; i >= 0; i-- {
		h = apitokens.Require(f.db, f.clk, scopes[i])(h)
	}
	return h
}

func callWith(h http.HandlerFunc, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/summary", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestSummaryReturnsDataWithBothScopes(t *testing.T) {
	f := setup(t)
	token := f.createToken(t, `"tasks:read","calendar:read"`)

	rec := callWith(f.summaryRoute("tasks:read", "calendar:read"), token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"date", "timezone", "tasks_due_today", "tasks_done_today", "tasks_overdue", "tasks_waiting_approval", "events_today", "next_events"} {
		if _, ok := resp.Data[key]; !ok {
			t.Fatalf("response missing data.%s: %s", key, rec.Body.String())
		}
	}
	if string(resp.Data["next_events"]) != "[]" {
		t.Fatalf("next_events should be an empty array, got %s", resp.Data["next_events"])
	}
	if string(resp.Data["tasks_due_today"]) != "0" {
		t.Fatalf("tasks_due_today should be 0 for an empty family, got %s", resp.Data["tasks_due_today"])
	}
}

func TestSummaryNeedsBothScopes(t *testing.T) {
	f := setup(t)
	token := f.createToken(t, `"tasks:read"`)

	rec := callWith(f.summaryRoute("tasks:read", "calendar:read"), token)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("token without calendar:read: status %d, want 403", rec.Code)
	}
}

func TestSummaryRejectsMissingRevokedAndExpiredTokens(t *testing.T) {
	f := setup(t)
	route := f.summaryRoute("tasks:read", "calendar:read")

	if rec := callWith(route, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401", rec.Code)
	}

	token := f.createToken(t, `"tasks:read","calendar:read"`)
	if _, err := f.db.Exec(`UPDATE api_tokens SET revoked_at = ? WHERE family_id = ?`, f.clk.Now(), f.family.FamilyID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if rec := callWith(route, token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token: status %d, want 401", rec.Code)
	}

	fresh := f.createToken(t, `"tasks:read","calendar:read"`)
	f.clk.Time = f.clk.Time.Add(31 * 24 * time.Hour)
	if rec := callWith(route, fresh); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired token: status %d, want 401", rec.Code)
	}
}
