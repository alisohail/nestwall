package rewards

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"nestwall/backend/internal/platform/clock"
)

// withSettingsRoutes adds the reward settings routes to the catalogue fixture's mux.
func withSettingsRoutes(f catFixture) catFixture {
	h := NewHandlers(f.db, clock.FakeClock{Time: catNow}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.mux.HandleFunc("GET /api/v1/rewards/settings", h.GetRewardSettings)
	f.mux.HandleFunc("PUT /api/v1/rewards/settings", h.PutRewardSettings)
	return f
}

func decodeRewardSettings(t *testing.T, body []byte) rewardSettings {
	t.Helper()
	var s rewardSettings
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("decode reward settings: %v", err)
	}
	return s
}

func TestRewardSettingsDefaultsToNoApproval(t *testing.T) {
	f := withSettingsRoutes(newCatFixture(t))
	rec := f.do(f.parent, http.MethodGet, "/api/v1/rewards/settings", "", nil)
	wantStatus(t, rec, http.StatusOK, "")
	if got := decodeRewardSettings(t, rec.Body.Bytes()); got.ApprovalRequiredDefault {
		t.Fatalf("default = %+v, want approval off", got)
	}
}

func TestRewardSettingsSaveAndRead(t *testing.T) {
	f := withSettingsRoutes(newCatFixture(t))

	wantStatus(t, f.do(f.parent, http.MethodPut, "/api/v1/rewards/settings",
		`{"approval_required_default":true}`, nil), http.StatusOK, "")
	rec := f.do(f.parent, http.MethodGet, "/api/v1/rewards/settings", "", nil)
	if got := decodeRewardSettings(t, rec.Body.Bytes()); !got.ApprovalRequiredDefault {
		t.Fatalf("after save = %+v, want approval on", got)
	}

	wantStatus(t, f.do(f.parent, http.MethodPut, "/api/v1/rewards/settings",
		`{"approval_required_default":false}`, nil), http.StatusOK, "")
	rec = f.do(f.parent, http.MethodGet, "/api/v1/rewards/settings", "", nil)
	if got := decodeRewardSettings(t, rec.Body.Bytes()); got.ApprovalRequiredDefault {
		t.Fatalf("after second save = %+v, want approval off", got)
	}
}

func TestRewardSettingsAreScopedToFamily(t *testing.T) {
	f := withSettingsRoutes(newCatFixture(t))
	wantStatus(t, f.do(f.parent, http.MethodPut, "/api/v1/rewards/settings",
		`{"approval_required_default":true}`, nil), http.StatusOK, "")

	rec := f.do(f.rival, http.MethodGet, "/api/v1/rewards/settings", "", nil)
	if got := decodeRewardSettings(t, rec.Body.Bytes()); got.ApprovalRequiredDefault {
		t.Fatalf("rival family saw %+v, want its own default off", got)
	}
}

func TestRewardSettingsRejectsBadBodies(t *testing.T) {
	f := withSettingsRoutes(newCatFixture(t))
	wantStatus(t, f.do(f.parent, http.MethodPut, "/api/v1/rewards/settings", `{}`, nil),
		http.StatusBadRequest, "validation_failed")
	wantStatus(t, f.do(f.parent, http.MethodPut, "/api/v1/rewards/settings",
		`{"approval_required_default":true,"extra":1}`, nil), http.StatusBadRequest, "bad_request")
}
