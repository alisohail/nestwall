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

func listDevices(f wallFixture, cookie *http.Cookie) *httptest.ResponseRecorder {
	return send(f.mux, http.MethodGet, "/api/v1/wall/devices", "", cookie, parentRemote)
}

func updateDevice(f wallFixture, id, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	return send(f.mux, http.MethodPatch, "/api/v1/wall/devices/"+id, body, cookie, parentRemote)
}

// listedDevices returns the parent's device list keyed by device id.
func listedDevices(t *testing.T, f wallFixture) map[string]deviceView {
	t.Helper()
	rec := listDevices(f, f.parent)
	if rec.Code != http.StatusOK {
		t.Fatalf("list devices: status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp listDevicesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode device list: %v", err)
	}
	byID := make(map[string]deviceView, len(resp.Devices))
	for _, d := range resp.Devices {
		byID[d.ID] = d
	}
	return byID
}

func TestListDevicesReportsStatusAndSkipsRevoked(t *testing.T) {
	f := newWallFixture(t)
	pending := createDevice(t, f, "Hall", "1234")
	online, _ := enrolledDevice(t, f, "Kitchen")
	revoked, _ := enrolledDevice(t, f, "Study")
	if rec := revokeDevice(f, revoked.ID, f.parent); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status %d, body %s", rec.Code, rec.Body.String())
	}

	devices := listedDevices(t, f)
	if _, ok := devices[revoked.ID]; ok {
		t.Fatal("revoked device is listed")
	}
	if got := devices[pending.ID].Status; got != "pending" {
		t.Fatalf("unpaired device status = %q, want pending", got)
	}
	if got := devices[online.ID].Status; got != "online" {
		t.Fatalf("just paired device status = %q, want online", got)
	}

	f.clk.advance(deviceOnlineWindow + time.Second)
	devices = listedDevices(t, f)
	if got := devices[online.ID].Status; got != "offline" {
		t.Fatalf("device silent past the window status = %q, want offline", got)
	}
	if devices[pending.ID].Status != "pending" {
		t.Fatal("pending device changed status while waiting for pairing")
	}
}

func TestListDevicesRefusesChild(t *testing.T) {
	f := newWallFixture(t)
	enrolledDevice(t, f, "Kitchen")

	if rec := listDevices(f, f.kid); rec.Code != http.StatusForbidden {
		t.Fatalf("child list: status %d, body %s; want 403", rec.Code, rec.Body.String())
	}
}

func TestUpdateDeviceChangesOnlySentFields(t *testing.T) {
	f := newWallFixture(t)
	dev, _ := enrolledDevice(t, f, "Kitchen")
	before := listedDevices(t, f)[dev.ID]

	rec := updateDevice(f, dev.ID, `{"name":"  Hall  "}`, f.parent)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rec.Code, rec.Body.String())
	}
	var got deviceView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if got.Name != "Hall" {
		t.Fatalf("name = %q, want trimmed Hall", got.Name)
	}
	if got.Status != before.Status || got.ID != dev.ID {
		t.Fatalf("update changed status or id: got %+v, before %+v", got, before)
	}
	if after := listedDevices(t, f)[dev.ID]; after.Name != "Hall" {
		t.Fatalf("list after update = %+v, want name Hall", after)
	}
}

func TestUpdateDeviceRejectsInvalidInput(t *testing.T) {
	f := newWallFixture(t)
	dev, _ := enrolledDevice(t, f, "Kitchen")

	cases := map[string]string{
		"empty name":      `{"name":"   "}`,
		"long name":       `{"name":"` + strings.Repeat("n", maxNameRunes+1) + `"}`,
		"short exit code": `{"exit_code":"123"}`,
		"long exit code":  `{"exit_code":"1234567"}`,
		"letters":         `{"exit_code":"12a4"}`,
	}
	for name, body := range cases {
		rec := updateDevice(f, dev.ID, body, f.parent)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, body %s; want 400", name, rec.Code, rec.Body.String())
		}
	}
	if got := listedDevices(t, f)[dev.ID]; got.Name != "Kitchen" {
		t.Fatalf("rejected update changed the name to %q", got.Name)
	}
}

func TestUpdateDeviceExitCodeIsHashedAndNotAudited(t *testing.T) {
	f := newWallFixture(t)
	dev, _ := enrolledDevice(t, f, "Kitchen")

	if rec := updateDevice(f, dev.ID, `{"exit_code":"5678"}`, f.parent); rec.Code != http.StatusOK {
		t.Fatalf("update exit code: status %d, body %s", rec.Code, rec.Body.String())
	}

	var exitHash string
	if err := f.db.QueryRow(`SELECT exit_code_hash FROM wall_devices WHERE id = ?`, dev.ID).Scan(&exitHash); err != nil {
		t.Fatalf("read exit code hash: %v", err)
	}
	if !auth.VerifyPassword("5678", exitHash) {
		t.Fatal("new exit code does not verify")
	}
	if auth.VerifyPassword("1234", exitHash) {
		t.Fatal("old exit code still verifies")
	}

	var action, metadata string
	if err := f.db.QueryRow(
		`SELECT action, metadata FROM audit_logs WHERE action = 'wall_device.updated' AND target_id = ?`, dev.ID,
	).Scan(&action, &metadata); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if !strings.Contains(metadata, "exit_code") {
		t.Fatalf("audit metadata %s does not name the changed field", metadata)
	}
	if strings.Contains(metadata, "5678") {
		t.Fatalf("audit metadata %s holds the exit code", metadata)
	}
}

func TestUpdateDeviceWithoutChangesWritesNoAudit(t *testing.T) {
	f := newWallFixture(t)
	dev, _ := enrolledDevice(t, f, "Kitchen")

	if rec := updateDevice(f, dev.ID, `{"name":"Kitchen"}`, f.parent); rec.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rec.Code, rec.Body.String())
	}
	var audits int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE action = 'wall_device.updated' AND target_id = ?`, dev.ID,
	).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 0 {
		t.Fatalf("unchanged update wrote %d audit rows", audits)
	}
}

func TestUpdateDeviceAccessRules(t *testing.T) {
	f := newWallFixture(t)
	dev, _ := enrolledDevice(t, f, "Kitchen")
	body := `{"name":"Hall"}`

	if rec := updateDevice(f, dev.ID, body, f.kid); rec.Code != http.StatusForbidden {
		t.Fatalf("child update: status %d, body %s; want 403", rec.Code, rec.Body.String())
	}
	if rec := updateDevice(f, "does-not-exist", body, f.parent); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: status %d, body %s; want 404", rec.Code, rec.Body.String())
	}
	if rec := revokeDevice(f, dev.ID, f.parent); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := updateDevice(f, dev.ID, body, f.parent); rec.Code != http.StatusNotFound {
		t.Fatalf("revoked device: status %d, body %s; want 404", rec.Code, rec.Body.String())
	}
}

func TestHeartbeatRefreshesLastSeen(t *testing.T) {
	useFastStreamIntervals(t)
	f := newWallFixture(t)
	dev, cookie := enrolledDevice(t, f, "Kitchen")

	stream := openStream(t, f, cookie)
	stream.expectLine(t, "retry: 3000", 2*time.Second)

	f.clk.advance(time.Minute + time.Second)
	want := f.clk.Now()

	lastSeen := func() sql.NullTime {
		t.Helper()
		var ts sql.NullTime
		if err := f.db.QueryRow(`SELECT last_seen_at FROM wall_devices WHERE id = ?`, dev.ID).Scan(&ts); err != nil {
			t.Fatalf("read last_seen_at: %v", err)
		}
		return ts
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if ts := lastSeen(); ts.Valid && ts.Time.Equal(want) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat did not refresh last_seen_at to %v (now %v)", want, lastSeen())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := listedDevices(t, f)[dev.ID].Status; got != "online" {
		t.Fatalf("device with a live stream status = %q, want online", got)
	}
}
