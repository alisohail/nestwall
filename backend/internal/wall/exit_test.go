package wall

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func verifyExit(f wallFixture, cookie *http.Cookie, code string) *httptest.ResponseRecorder {
	return send(f.mux, http.MethodPost, "/api/v1/wall/exit/verify", `{"exit_code":"`+code+`"}`, cookie, deviceRemote)
}

func unpairDevice(f wallFixture, cookie *http.Cookie, code string) *httptest.ResponseRecorder {
	return send(f.mux, http.MethodPost, "/api/v1/wall/unpair", `{"exit_code":"`+code+`"}`, cookie, deviceRemote)
}

func failedAttempts(t *testing.T, f wallFixture, deviceID string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT failed_exit_attempts FROM wall_devices WHERE id = ?`, deviceID).Scan(&n); err != nil {
		t.Fatalf("read failed attempts: %v", err)
	}
	return n
}

func TestExitCodeAcceptsCorrectCodeAndResetsCounter(t *testing.T) {
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	rec := verifyExit(f, cookie, "9999")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"attempts_left":4`) {
		t.Fatalf("wrong code: status %d, body %s; want 403 with 4 attempts left", rec.Code, rec.Body.String())
	}
	if n := failedAttempts(t, f, dev.ID); n != 1 {
		t.Fatalf("failed attempts = %d, want 1", n)
	}

	if rec := verifyExit(f, cookie, "1234"); rec.Code != http.StatusNoContent {
		t.Fatalf("correct code: status %d, body %s", rec.Code, rec.Body.String())
	}
	if n := failedAttempts(t, f, dev.ID); n != 0 {
		t.Fatalf("failed attempts after success = %d, want 0", n)
	}
}

func TestExitCodeLocksAfterFiveWrongAttempts(t *testing.T) {
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	for i := 0; i < exitMaxAttempts-1; i++ {
		if rec := verifyExit(f, cookie, "0000"); rec.Code != http.StatusForbidden {
			t.Fatalf("attempt %d: status %d, body %s; want 403", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := verifyExit(f, cookie, "0000")
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "exit_locked") {
		t.Fatalf("fifth attempt: status %d, body %s; want 429 exit_locked", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Params map[string]float64 `json:"params"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode lock response: %v", err)
	}
	if got := body.Error.Params["retry_after"]; got != exitLockout.Seconds() {
		t.Fatalf("retry_after = %v, want %v", got, exitLockout.Seconds())
	}

	if rec := verifyExit(f, cookie, "1234"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("correct code while locked: status %d, body %s; want 429", rec.Code, rec.Body.String())
	}

	f.clk.advance(exitLockout + time.Second)
	if rec := verifyExit(f, cookie, "1234"); rec.Code != http.StatusNoContent {
		t.Fatalf("correct code after lockout: status %d, body %s", rec.Code, rec.Body.String())
	}
	if n := failedAttempts(t, f, dev.ID); n != 0 {
		t.Fatalf("failed attempts after lockout expired = %d, want 0", n)
	}
}

func TestExitCodeRejectsMalformedCodeWithoutCounting(t *testing.T) {
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	for _, code := range []string{"12", "1234567", "12a4", ""} {
		rec := verifyExit(f, cookie, code)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "validation_failed") {
			t.Fatalf("code %q: status %d, body %s; want 400 validation_failed", code, rec.Code, rec.Body.String())
		}
	}
	if n := failedAttempts(t, f, dev.ID); n != 0 {
		t.Fatalf("malformed codes were counted: %d", n)
	}
}

func TestExitCodeRefusesMissingAndSessionCredentials(t *testing.T) {
	f := newWallFixture(t)
	enrolledDevice(t, f, "Kitchen")

	cases := map[string]*http.Cookie{
		"no cookie":      nil,
		"parent session": f.parent,
		"child session":  f.kid,
	}
	for name, cookie := range cases {
		if rec := verifyExit(f, cookie, "1234"); rec.Code != http.StatusUnauthorized {
			t.Errorf("verify, %s: status %d, body %s; want 401", name, rec.Code, rec.Body.String())
		}
		if rec := unpairDevice(f, cookie, "1234"); rec.Code != http.StatusUnauthorized {
			t.Errorf("unpair, %s: status %d, body %s; want 401", name, rec.Code, rec.Body.String())
		}
	}
}

func TestExitResponsesNeverEchoTheCode(t *testing.T) {
	f := newWallFixture(t)
	_, cookie := enrolledDevice(t, f, "Kitchen")

	for _, code := range []string{"1234", "9999"} {
		rec := verifyExit(f, cookie, code)
		if strings.Contains(rec.Body.String(), "1234") || strings.Contains(rec.Body.String(), "exit_code_hash") {
			t.Fatalf("response for %s leaks the code or hash: %s", code, rec.Body.String())
		}
	}
}

func TestUnpairRequiresExitCodeAndRevokesDevice(t *testing.T) {
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	if rec := unpairDevice(f, cookie, "0000"); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong code: status %d, body %s; want 403", rec.Code, rec.Body.String())
	}
	if rec := getDevice(f, cookie); rec.Code != http.StatusOK {
		t.Fatalf("wrong code unpaired the device: status %d, body %s", rec.Code, rec.Body.String())
	}

	if rec := unpairDevice(f, cookie, "1234"); rec.Code != http.StatusNoContent {
		t.Fatalf("correct code: status %d, body %s; want 204", rec.Code, rec.Body.String())
	}

	rec := getDevice(f, cookie)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "device_invalid") {
		t.Fatalf("after unpair: status %d, body %s; want 401 device_invalid", rec.Code, rec.Body.String())
	}
	if rec := verifyExit(f, cookie, "1234"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("verify after unpair: status %d, body %s; want 401", rec.Code, rec.Body.String())
	}

	var revoked int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM wall_devices WHERE id = ? AND revoked_at IS NOT NULL`, dev.ID).Scan(&revoked); err != nil {
		t.Fatalf("read revoked: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("device not revoked after unpair")
	}
}

func TestUnpairAuditNamesCreatorAndDeviceInitiator(t *testing.T) {
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	if rec := unpairDevice(f, cookie, "1234"); rec.Code != http.StatusNoContent {
		t.Fatalf("unpair: status %d, body %s", rec.Code, rec.Body.String())
	}

	var action, metadata string
	var actor string
	if err := f.db.QueryRow(
		`SELECT action, actor_id, metadata FROM audit_logs WHERE action = 'wall_device.unpaired' AND target_id = ?`, dev.ID,
	).Scan(&action, &actor, &metadata); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if !strings.Contains(metadata, `"initiated_by":"wall_device"`) {
		t.Fatalf("audit metadata %s does not record the device as initiator", metadata)
	}
	var creator string
	if err := f.db.QueryRow(`SELECT created_by FROM wall_devices WHERE id = ?`, dev.ID).Scan(&creator); err != nil {
		t.Fatalf("read creator: %v", err)
	}
	if actor != creator {
		t.Fatalf("audit actor %q, want creator %q", actor, creator)
	}
}

func TestUnpairedDeviceCannotBeRevokedTwice(t *testing.T) {
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	if rec := unpairDevice(f, cookie, "1234"); rec.Code != http.StatusNoContent {
		t.Fatalf("unpair: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := revokeDevice(f, dev.ID, f.parent); rec.Code != http.StatusNotFound {
		t.Fatalf("revoke after unpair: status %d, body %s; want 404", rec.Code, rec.Body.String())
	}
}
