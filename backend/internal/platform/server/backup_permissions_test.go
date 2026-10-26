package server

import (
	"net/http"
	"testing"

	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

// TestBackupRoutesNeedTheirOwnPermission checks that settings.manage alone does not
// open the backup, restore or factory reset routes. Each route has its own
// permission (backup.manage, backup.restore or data.reset).
func TestBackupRoutesNeedTheirOwnPermission(t *testing.T) {
	conn := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, conn)
	childCookie := addChild(t, conn, fam.FamilyID, "Ava")
	h := newIsolationServer(conn)
	child := newIsolationClient(t, h, childCookie)

	var childMemberID string
	if err := conn.QueryRow(
		`SELECT id FROM family_members WHERE family_id = ? AND role_id = ?`, fam.FamilyID, childRoleID,
	).Scan(&childMemberID); err != nil {
		t.Fatalf("find child member: %v", err)
	}
	grant := func(code string) {
		t.Helper()
		if _, err := conn.Exec(
			`INSERT INTO member_permission_overrides (id, family_member_id, permission_id, access_level)
			 VALUES (?, ?, (SELECT id FROM permissions WHERE code = ?), 'full')`,
			ids.New(), childMemberID, code,
		); err != nil {
			t.Fatalf("grant %s: %v", code, err)
		}
	}
	grant("settings.manage")

	routes := []struct {
		name   string
		method string
		path   string
		body   string
		perm   string
	}{
		{"status", http.MethodGet, "/api/v1/settings/backups", "", "backup.manage"},
		{"run now", http.MethodPost, "/api/v1/settings/backups/run", "", "backup.manage"},
		{"download", http.MethodPost, "/api/v1/settings/backups/x/download", `{"password":"x"}`, "backup.manage"},
		{"restore", http.MethodPost, "/api/v1/settings/backups/x/restore", `{"confirm":"x"}`, "backup.restore"},
		{"restore upload", http.MethodPost, "/api/v1/settings/backups/restore-upload", "", "backup.restore"},
		{"reset preview", http.MethodGet, "/api/v1/settings/reset/preview", "", "data.reset"},
		{"factory reset", http.MethodPost, "/api/v1/settings/reset", `{"password":"x","confirm":"x"}`, "data.reset"},
	}

	for _, r := range routes {
		t.Run(r.name+" denied with settings.manage only", func(t *testing.T) {
			rec := child.do(r.method, r.path, r.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
			}
		})
	}

	// The child gets past the gate once the route's own permission is granted.
	// The preview is a read, so it can return 200 with only data.reset.
	grant("data.reset")
	t.Run("reset preview allowed with data.reset", func(t *testing.T) {
		rec := child.do(http.MethodGet, "/api/v1/settings/reset/preview", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})
}
