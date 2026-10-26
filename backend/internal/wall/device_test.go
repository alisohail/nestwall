package wall

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/auth"
)

const deviceRemote = "203.0.113.5:4000"

// enrolledDevice creates a device, pairs it, and returns it with its device credential cookie.
func enrolledDevice(t *testing.T, f wallFixture, name string) (createDeviceResponse, *http.Cookie) {
	t.Helper()
	dev := createDevice(t, f, name, "1234")
	rec := pair(f, dev.PairingKey, deviceRemote)
	if rec.Code != http.StatusOK {
		t.Fatalf("pair: status %d, body %s", rec.Code, rec.Body.String())
	}
	cookie := findCookie(rec, deviceCookie)
	if cookie == nil {
		t.Fatal("pair did not set the device cookie")
	}
	return dev, cookie
}

func getDevice(f wallFixture, cookie *http.Cookie) *httptest.ResponseRecorder {
	return send(f.mux, http.MethodGet, "/api/v1/wall/device", "", cookie, deviceRemote)
}

func revokeDevice(f wallFixture, id string, cookie *http.Cookie) *httptest.ResponseRecorder {
	return send(f.mux, http.MethodPost, "/api/v1/wall/devices/"+id+"/revoke", "", cookie, parentRemote)
}

func TestPairedDeviceReachesDeviceEndpoint(t *testing.T) {
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	rec := getDevice(f, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("device endpoint: status %d, body %s", rec.Code, rec.Body.String())
	}
	var got deviceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode device response: %v", err)
	}
	if got.ID != dev.ID || got.Name != "Kitchen" {
		t.Fatalf("device = %+v, want %s Kitchen", got, dev.ID)
	}
}

func TestDeviceEndpointRefusesMissingAndSessionCredentials(t *testing.T) {
	f := newWallFixture(t)
	enrolledDevice(t, f, "Kitchen")

	cases := map[string]*http.Cookie{
		"no cookie":      nil,
		"parent session": f.parent,
		"child session":  f.kid,
	}
	for name, cookie := range cases {
		rec := getDevice(f, cookie)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "unauthenticated") {
			t.Errorf("%s: status %d, body %s; want 401 unauthenticated", name, rec.Code, rec.Body.String())
		}
	}
}

func TestDeviceCredentialCannotCallSessionEndpoints(t *testing.T) {
	f := newWallFixture(t)
	_, cookie := enrolledDevice(t, f, "Kitchen")

	body := `{"name":"Hall","exit_code":"1234"}`
	rec := send(f.mux, http.MethodPost, "/api/v1/wall/devices", body, cookie, deviceRemote)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "unauthenticated") {
		t.Fatalf("status %d, body %s; want 401 unauthenticated", rec.Code, rec.Body.String())
	}

	var devices int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM wall_devices`).Scan(&devices); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if devices != 1 {
		t.Fatalf("device credential created a device: %d devices", devices)
	}
}

func TestPairingKeyIsNotADeviceCredential(t *testing.T) {
	f := newWallFixture(t)
	dev := createDevice(t, f, "Hall", "1234")

	for _, value := range []string{dev.PairingKey, normalizeKey(dev.PairingKey)} {
		rec := getDevice(f, &http.Cookie{Name: deviceCookie, Value: value})
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "device_invalid") {
			t.Fatalf("pairing key %q as credential: status %d, body %s; want 401 device_invalid",
				value, rec.Code, rec.Body.String())
		}
	}
}

func TestRevokedDeviceIsRefusedOnNextRequest(t *testing.T) {
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	if rec := getDevice(f, cookie); rec.Code != http.StatusOK {
		t.Fatalf("before revoke: status %d, body %s", rec.Code, rec.Body.String())
	}

	if rec := revokeDevice(f, dev.ID, f.parent); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec := getDevice(f, cookie)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "device_invalid") {
		t.Fatalf("after revoke: status %d, body %s; want 401 device_invalid", rec.Code, rec.Body.String())
	}

	var audits int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE action = 'wall_device.revoked' AND target_id = ?`, dev.ID,
	).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 1 {
		t.Fatalf("revoke audit rows = %d, want 1", audits)
	}
}

func TestChildCannotRevokeDevice(t *testing.T) {
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	rec := revokeDevice(f, dev.ID, f.kid)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("child revoke: status %d, body %s; want 403", rec.Code, rec.Body.String())
	}
	if rec := getDevice(f, cookie); rec.Code != http.StatusOK {
		t.Fatalf("device after refused revoke: status %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeUnknownOrAlreadyRevokedDeviceReturnsNotFound(t *testing.T) {
	f := newWallFixture(t)
	dev, _ := enrolledDevice(t, f, "Kitchen")

	if rec := revokeDevice(f, "does-not-exist", f.parent); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: status %d, body %s; want 404", rec.Code, rec.Body.String())
	}
	if rec := revokeDevice(f, dev.ID, f.parent); rec.Code != http.StatusNoContent {
		t.Fatalf("first revoke: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := revokeDevice(f, dev.ID, f.parent); rec.Code != http.StatusNotFound {
		t.Fatalf("second revoke: status %d, body %s; want 404", rec.Code, rec.Body.String())
	}
}

func TestDeviceRevokedBeforePairingCannotPair(t *testing.T) {
	f := newWallFixture(t)
	dev := createDevice(t, f, "Hall", "1234")

	if rec := revokeDevice(f, dev.ID, f.parent); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status %d, body %s", rec.Code, rec.Body.String())
	}
	assertRejected(t, pair(f, dev.PairingKey, deviceRemote))
}

func TestLastSeenIsRecordedAndThrottled(t *testing.T) {
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")
	pairedAt := f.clk.now

	lastSeen := func() time.Time {
		t.Helper()
		var ts sql.NullTime
		if err := f.db.QueryRow(`SELECT last_seen_at FROM wall_devices WHERE id = ?`, dev.ID).Scan(&ts); err != nil {
			t.Fatalf("read last_seen_at: %v", err)
		}
		if !ts.Valid {
			t.Fatal("last_seen_at is NULL")
		}
		return ts.Time
	}

	if got := lastSeen(); !got.Equal(pairedAt) {
		t.Fatalf("after pairing last_seen_at = %v, want %v", got, pairedAt)
	}

	f.clk.now = pairedAt.Add(30 * time.Second)
	if rec := getDevice(f, cookie); rec.Code != http.StatusOK {
		t.Fatalf("request inside throttle window: status %d, body %s", rec.Code, rec.Body.String())
	}
	if got := lastSeen(); !got.Equal(pairedAt) {
		t.Fatalf("inside throttle window last_seen_at = %v, want unchanged %v", got, pairedAt)
	}

	f.clk.now = pairedAt.Add(time.Minute + time.Second)
	if rec := getDevice(f, cookie); rec.Code != http.StatusOK {
		t.Fatalf("request after throttle window: status %d, body %s", rec.Code, rec.Body.String())
	}
	if want := f.clk.now; !lastSeen().Equal(want) {
		t.Fatalf("after throttle window last_seen_at = %v, want %v", lastSeen(), want)
	}
}

func TestDeviceCookieValueIsHashedOnlyWhenStored(t *testing.T) {
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	var stored string
	if err := f.db.QueryRow(`SELECT credential_hash FROM wall_devices WHERE id = ?`, dev.ID).Scan(&stored); err != nil {
		t.Fatalf("read credential hash: %v", err)
	}
	if stored == cookie.Value {
		t.Fatal("device credential stored in plaintext")
	}
	if want := auth.HashToken(cookie.Value); stored != want {
		t.Fatalf("stored credential hash = %s, want %s", stored, want)
	}
}
