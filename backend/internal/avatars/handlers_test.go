package avatars

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

// actor is a signed-in family member.
type actor struct {
	memberID string
	userID   string
}

type fixture struct {
	db     *sql.DB
	h      *Handlers
	dir    string
	family testhelper.Family
	parent actor
	child  actor
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	dir := filepath.Join(t.TempDir(), "avatars")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := fixture{
		db:     db,
		h:      NewHandlers(db, dir, logger),
		dir:    dir,
		family: fam,
		parent: actor{memberID: fam.MemberID, userID: fam.ParentUserID},
	}
	f.child = f.addMember(t, "Child")
	return f
}

// addMember adds an active member with the named seeded role to the fixture family.
func (f fixture) addMember(t *testing.T, roleName string) actor {
	t.Helper()
	ctx := context.Background()
	var roleID string
	if err := f.db.QueryRowContext(ctx, `SELECT id FROM roles WHERE name = ?`, roleName).Scan(&roleID); err != nil {
		t.Fatalf("find role %s: %v", roleName, err)
	}
	userID := ids.New()
	userCode := "u_" + userID[:8]
	if _, err := f.db.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, '', ?, ?)`,
		userID, userCode, roleName, boolInt(roleName == "Child"),
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	memberID := ids.New()
	if _, err := f.db.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour) VALUES (?, ?, ?, ?, 'active', '#F00')`,
		memberID, f.family.FamilyID, userID, roleID,
	); err != nil {
		t.Fatalf("insert family member: %v", err)
	}
	return actor{memberID: memberID, userID: userID}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func withActor(req *http.Request, a actor) *http.Request {
	ctx := context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, a.memberID)
	ctx = context.WithValue(ctx, permissions.ContextKeyUserID, a.userID)
	return req.WithContext(ctx)
}

func multipartBody(t *testing.T, file []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	if file != nil {
		fw, err := mw.CreateFormFile("file", "picture.jpg")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := fw.Write(file); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

func (f fixture) upload(t *testing.T, a actor, targetID string, file []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartBody(t, file, fields)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/family/members/"+targetID+"/avatar", body)
	req.Header.Set("Content-Type", contentType)
	req.SetPathValue("id", targetID)
	rec := httptest.NewRecorder()
	f.h.Upload(rec, withActor(req, a))
	return rec
}

func (f fixture) remove(t *testing.T, a actor, targetID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/family/members/"+targetID+"/avatar", nil)
	req.SetPathValue("id", targetID)
	rec := httptest.NewRecorder()
	f.h.Delete(rec, withActor(req, a))
	return rec
}

func (f fixture) serve(t *testing.T, a actor, avatarID, size string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/v1/avatars/" + avatarID
	if size != "" {
		url += "?size=" + size
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", avatarID)
	rec := httptest.NewRecorder()
	f.h.Serve(rec, withActor(req, a))
	return rec
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, want, rec.Body.String())
	}
}

func expectErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != want {
		t.Fatalf("error code = %q, want %q (status %d)", resp.Error.Code, want, rec.Code)
	}
}

func avatarIDOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if resp["avatar_id"] == "" {
		t.Fatalf("missing avatar_id in %s", rec.Body.String())
	}
	return resp["avatar_id"]
}

func (f fixture) rowCount(t *testing.T, memberID string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM member_avatars WHERE family_member_id = ?`, memberID).Scan(&n); err != nil {
		t.Fatalf("count avatars: %v", err)
	}
	return n
}

func (f fixture) setFamilyFlag(t *testing.T, column string, value int) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE families SET `+column+` = ? WHERE id = ?`, value, f.family.FamilyID); err != nil {
		t.Fatalf("set %s: %v", column, err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func TestUploadStoresTwoSizesAndReplacesOld(t *testing.T) {
	f := newFixture(t)

	rec := f.upload(t, f.parent, f.parent.memberID, pngBytes(t, 300, 200, false), nil)
	expectStatus(t, rec, http.StatusOK)
	first := avatarIDOf(t, rec)
	if got := decodedSize(t, readFile(t, filepath.Join(f.dir, fileName(first, LargeSize)))); got != image.Pt(LargeSize, LargeSize) {
		t.Errorf("large file = %v", got)
	}
	if got := decodedSize(t, readFile(t, filepath.Join(f.dir, fileName(first, SmallSize)))); got != image.Pt(SmallSize, SmallSize) {
		t.Errorf("small file = %v", got)
	}

	rec = f.upload(t, f.parent, f.parent.memberID, pngBytes(t, 120, 120, false), map[string]string{"x": "10", "y": "10", "size": "100"})
	expectStatus(t, rec, http.StatusOK)
	second := avatarIDOf(t, rec)
	if second == first {
		t.Fatalf("second upload reused the avatar ID")
	}
	for _, size := range []int{LargeSize, SmallSize} {
		if _, err := os.Stat(filepath.Join(f.dir, fileName(first, size))); !os.IsNotExist(err) {
			t.Errorf("old %d px file still exists: %v", size, err)
		}
	}
	if n := f.rowCount(t, f.parent.memberID); n != 1 {
		t.Errorf("avatar rows = %d, want 1", n)
	}
}

func TestUploadRejectsUnsafeOrUnsupportedFiles(t *testing.T) {
	cases := []struct {
		name   string
		file   []byte
		status int
		code   string
	}{
		{"text renamed to jpg", []byte("not a picture"), http.StatusBadRequest, "unsupported_type"},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`), http.StatusBadRequest, "unsupported_type"},
		{"animated png", animatedPNG(t), http.StatusBadRequest, "animated_image"},
		{"over 5 MB", bytes.Repeat([]byte{0}, MaxUploadBytes+1), http.StatusRequestEntityTooLarge, "image_too_large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			rec := f.upload(t, f.parent, f.parent.memberID, tc.file, nil)
			expectStatus(t, rec, tc.status)
			expectErrorCode(t, rec, tc.code)
			if n := f.rowCount(t, f.parent.memberID); n != 0 {
				t.Errorf("rejected upload stored %d rows", n)
			}
			if entries, _ := os.ReadDir(f.dir); len(entries) != 0 {
				t.Errorf("rejected upload left %d files", len(entries))
			}
		})
	}
}

func TestMembersChangeOwnPictureOnlyWhenAllowed(t *testing.T) {
	f := newFixture(t)
	png := pngBytes(t, 50, 50, false)

	expectStatus(t, f.upload(t, f.child, f.child.memberID, png, nil), http.StatusOK)

	f.setFamilyFlag(t, "members_can_change_avatar", 0)
	rec := f.upload(t, f.child, f.child.memberID, png, nil)
	expectStatus(t, rec, http.StatusForbidden)
	expectErrorCode(t, rec, "avatar_changes_locked")

	// A parent can still change a child's picture when members are locked out.
	expectStatus(t, f.upload(t, f.parent, f.child.memberID, png, nil), http.StatusOK)
}

func TestChildCannotChangeAnotherMembersPicture(t *testing.T) {
	f := newFixture(t)
	png := pngBytes(t, 50, 50, false)
	expectStatus(t, f.upload(t, f.parent, f.parent.memberID, png, nil), http.StatusOK)

	rec := f.upload(t, f.child, f.parent.memberID, png, nil)
	expectStatus(t, rec, http.StatusForbidden)
	expectErrorCode(t, rec, "forbidden")

	rec = f.remove(t, f.child, f.parent.memberID)
	expectStatus(t, rec, http.StatusForbidden)
	expectErrorCode(t, rec, "forbidden")
}

func TestServeIsLimitedToFamilyAndWallSetting(t *testing.T) {
	f := newFixture(t)
	expectStatus(t, f.upload(t, f.parent, f.parent.memberID, pngBytes(t, 300, 200, false), nil), http.StatusOK)
	var avatarID string
	if err := f.db.QueryRow(`SELECT id FROM member_avatars WHERE family_member_id = ?`, f.parent.memberID).Scan(&avatarID); err != nil {
		t.Fatalf("read avatar id: %v", err)
	}

	rec := f.serve(t, f.child, avatarID, "96")
	expectStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("content type = %q, want image/jpeg", ct)
	}
	if got := decodedSize(t, rec.Body.Bytes()); got != image.Pt(SmallSize, SmallSize) {
		t.Errorf("served size = %v, want 96x96", got)
	}

	expectStatus(t, f.serve(t, f.child, avatarID, ""), http.StatusOK)
	expectStatus(t, f.serve(t, f.child, avatarID, "512"), http.StatusBadRequest)

	other := testhelper.OneFamily(t, f.db)
	expectStatus(t, f.serve(t, actor{memberID: other.MemberID, userID: other.ParentUserID}, avatarID, "96"), http.StatusNotFound)
	expectStatus(t, f.serve(t, f.parent, ids.New(), "96"), http.StatusNotFound)

	f.setFamilyFlag(t, "show_avatars_on_wall", 0)
	expectStatus(t, f.serve(t, f.child, avatarID, "96"), http.StatusOK)
}

func TestDeleteRemovesPictureAndFiles(t *testing.T) {
	f := newFixture(t)
	rec := f.upload(t, f.parent, f.parent.memberID, pngBytes(t, 80, 80, false), nil)
	expectStatus(t, rec, http.StatusOK)
	id := avatarIDOf(t, rec)

	expectStatus(t, f.remove(t, f.parent, f.parent.memberID), http.StatusNoContent)
	if n := f.rowCount(t, f.parent.memberID); n != 0 {
		t.Errorf("avatar rows = %d, want 0", n)
	}
	for _, size := range []int{LargeSize, SmallSize} {
		if _, err := os.Stat(filepath.Join(f.dir, fileName(id, size))); !os.IsNotExist(err) {
			t.Errorf("%d px file still exists: %v", size, err)
		}
	}
	expectStatus(t, f.remove(t, f.parent, f.parent.memberID), http.StatusNoContent)
}

func TestSettingsReadAndPartialUpdate(t *testing.T) {
	f := newFixture(t)

	req := withActor(httptest.NewRequest(http.MethodPut, "/api/v1/family/avatar-settings",
		strings.NewReader(`{"show_avatars_on_wall": false}`)), f.parent)
	rec := httptest.NewRecorder()
	f.h.UpdateSettings(rec, req)
	expectStatus(t, rec, http.StatusOK)

	req = withActor(httptest.NewRequest(http.MethodGet, "/api/v1/family/avatar-settings", nil), f.parent)
	rec = httptest.NewRecorder()
	f.h.GetSettings(rec, req)
	expectStatus(t, rec, http.StatusOK)
	var got SettingsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if got.ShowAvatarsOnWall {
		t.Errorf("show_avatars_on_wall = true, want false")
	}
	if !got.MembersCanChangeAvatar {
		t.Errorf("members_can_change_avatar changed by a partial update")
	}
}
