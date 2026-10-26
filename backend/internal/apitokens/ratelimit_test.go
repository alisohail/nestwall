package apitokens

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const validCreateBody = `{"name":"Home Assistant","scopes":["tasks:read"],"expires_in_days":30}`

func TestCreateIsRateLimitedPerMember(t *testing.T) {
	f := setup(t)

	for i := 0; i < createMaxAttempts; i++ {
		f.create(t, validCreateBody)
	}

	req := f.asMember(httptest.NewRequest(http.MethodPost, "/api/v1/settings/api-tokens", strings.NewReader(validCreateBody)), f.family.MemberID)
	rec := httptest.NewRecorder()
	f.h.Create(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over limit: status %d, want 429; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("over limit: body %s missing rate_limited code", rec.Body.String())
	}

	f.clk.Time = f.clk.Time.Add(createWindow + time.Minute)
	f.create(t, validCreateBody)
}
