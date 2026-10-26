package wall

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"nestwall/backend/internal/platform/ids"
)

// addPicture stores a picture for a member and writes its 96 px file into the avatar folder.
func addPicture(t *testing.T, s summaryFx, memberID string) string {
	t.Helper()
	id := ids.New()
	mustExec(t, s.db, `INSERT INTO member_avatars (id, family_id, family_member_id) VALUES (?, ?, ?)`, id, s.familyID, memberID)
	if err := os.WriteFile(filepath.Join(s.avatarDir, id+"-96.jpg"), []byte("picture-96"), 0o600); err != nil {
		t.Fatalf("write picture: %v", err)
	}
	return id
}

func TestWallServesPictureToPairedDevice(t *testing.T) {
	s := newSummaryFx(t)
	s.setOptions(t, summaryOptions{ShowPictures: true})
	id := addPicture(t, s, s.parentMemberID)

	rec := send(s.mux, http.MethodGet, "/api/v1/wall/avatars/"+id+"?size=96", "", s.cookie, deviceRemote)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "picture-96" {
		t.Fatalf("body = %q, want the stored picture", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content type = %q, want image/jpeg", ct)
	}
}

func TestWallPictureIsHiddenWhenNotAllowed(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, s summaryFx)
	}{
		{"device hides pictures", func(t *testing.T, s summaryFx) {
			mustExec(t, s.db, `UPDATE wall_devices SET show_pictures = 0 WHERE id = ?`, s.deviceID)
		}},
		{"family hides avatars on the wall", func(t *testing.T, s summaryFx) {
			mustExec(t, s.db, `UPDATE families SET show_avatars_on_wall = 0 WHERE id = ?`, s.familyID)
		}},
		{"member is inactive", func(t *testing.T, s summaryFx) {
			mustExec(t, s.db, `UPDATE family_members SET status = 'inactive' WHERE id = ?`, s.parentMemberID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSummaryFx(t)
			s.setOptions(t, summaryOptions{ShowPictures: true})
			id := addPicture(t, s, s.parentMemberID)
			tc.setup(t, s)

			rec := send(s.mux, http.MethodGet, "/api/v1/wall/avatars/"+id+"?size=96", "", s.cookie, deviceRemote)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
		})
	}
}

func TestWallPictureRefusesBadRequests(t *testing.T) {
	s := newSummaryFx(t)
	s.setOptions(t, summaryOptions{ShowPictures: true})
	id := addPicture(t, s, s.parentMemberID)

	if rec := send(s.mux, http.MethodGet, "/api/v1/wall/avatars/"+id+"?size=100", "", s.cookie, deviceRemote); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad size: status = %d, want 400", rec.Code)
	}
	if rec := send(s.mux, http.MethodGet, "/api/v1/wall/avatars/"+id+"?size=96", "", nil, deviceRemote); rec.Code == http.StatusOK {
		t.Fatalf("anonymous request: status = %d, want it refused", rec.Code)
	}
	if rec := send(s.mux, http.MethodGet, "/api/v1/wall/avatars/unknown-id?size=96", "", s.cookie, deviceRemote); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown picture: status = %d, want 404", rec.Code)
	}
}
