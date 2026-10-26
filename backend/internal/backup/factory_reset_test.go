package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nestwall/backend/internal/demo"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
)

// seedDemo fills a fresh database with the demo family and returns the parent password.
func seedDemo(t *testing.T, conn *sql.DB) string {
	t.Helper()
	seeded, err := demo.SeedWithPassword(context.Background(), conn, clock.RealClock{}, "")
	if err != nil {
		t.Fatalf("seed demo family: %v", err)
	}
	return seeded.Password
}

func TestFactoryResetAbortsWhenBackupFails(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedDemo(t, conn)
	before := sqlCount(t, conn, `SELECT COUNT(*) FROM family_members`)

	// A file where the backup folder should be makes the backup fail before anything is written.
	blocker := filepath.Join(t.TempDir(), "blocker")
	writeFile(t, blocker, []byte("not a folder"))

	_, err := FactoryReset(ctx, conn, ResetOptions{
		Dir:        filepath.Join(blocker, "backups"),
		DBKey:      testDBKey,
		AvatarsDir: filepath.Join(t.TempDir(), "avatars"),
	})
	if !errors.Is(err, ErrResetBackupFailed) {
		t.Fatalf("err = %v, want ErrResetBackupFailed", err)
	}
	if after := sqlCount(t, conn, `SELECT COUNT(*) FROM family_members`); after != before {
		t.Fatalf("family members = %d, want %d: data was removed after a failed backup", after, before)
	}
}

func TestFactoryResetKeepsBackupPassphrase(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedDemo(t, conn)
	if _, err := conn.Exec(`UPDATE backup_settings SET passphrase_sealed = 'sealed-test', keep_count = 3 WHERE id = 1`); err != nil {
		t.Fatalf("seed backup settings: %v", err)
	}
	root := t.TempDir()

	if _, err := FactoryReset(ctx, conn, ResetOptions{Dir: filepath.Join(root, "backups"), DBKey: testDBKey, AvatarsDir: filepath.Join(root, "avatars")}); err != nil {
		t.Fatalf("FactoryReset: %v", err)
	}

	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_settings WHERE passphrase_sealed = 'sealed-test'`); n != 1 {
		t.Error("the backup passphrase was dropped; the reset archive could not be opened")
	}
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_settings WHERE keep_count = 6`); n != 1 {
		t.Error("keep count did not go back to its default")
	}
}

func TestFactoryResetRemovesFamilyDataAndKeepsReference(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedDemo(t, conn)
	root := t.TempDir()
	avatars := filepath.Join(root, "avatars")
	writeFile(t, filepath.Join(avatars, "sam.png"), []byte("sam avatar"))
	dir := filepath.Join(root, "backups")

	res, err := FactoryReset(ctx, conn, ResetOptions{Dir: dir, DBKey: testDBKey, AvatarsDir: avatars})
	if err != nil {
		t.Fatalf("FactoryReset: %v", err)
	}

	for _, table := range []string{"families", "users", "family_members", "task_definitions", "points_ledger", "member_avatars"} {
		if n := sqlCount(t, conn, `SELECT COUNT(*) FROM `+table); n != 0 {
			t.Errorf("%s rows = %d, want 0", table, n)
		}
	}
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM roles`); n == 0 {
		t.Error("roles were removed; first-run setup needs them")
	}
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM permissions`); n == 0 {
		t.Error("permissions were removed; first-run setup needs them")
	}
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_settings WHERE keep_count = 6 AND encrypt = 0 AND schedule_hour = 2`); n != 1 {
		t.Errorf("backup settings not back to defaults")
	}
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_runs`); n != 1 {
		t.Errorf("backup_runs rows = %d, want only the reset's own backup", n)
	}
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_runs WHERE id = ? AND kind = 'before_reset' AND status = 'ok'`, res.ID); n != 1 {
		t.Errorf("reset backup row missing or not ok")
	}
	if _, err := os.Stat(avatars); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("avatars folder still exists: %v", err)
	}

	if len(res.FileName) < len(resetPrefix) || res.FileName[:len(resetPrefix)] != resetPrefix {
		t.Fatalf("reset archive %q does not use the reset prefix", res.FileName)
	}
	if err := Verify(filepath.Join(dir, res.FileName), VerifyOptions{DBKey: testDBKey}); err != nil {
		t.Fatalf("reset archive does not verify: %v", err)
	}
}

func TestResetRetentionDeletesOnlyOldResetArchives(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	old := resetPrefix + now.Add(-31*24*time.Hour).Format(timeLayout) + Extension
	recent := resetPrefix + now.Add(-29*24*time.Hour).Format(timeLayout) + Extension
	normal := filePrefix + now.Add(-90*24*time.Hour).Format(timeLayout) + Extension
	newerNormal := filePrefix + now.Add(-24*time.Hour).Format(timeLayout) + Extension
	for _, name := range []string{old, recent, normal, newerNormal} {
		writeFile(t, filepath.Join(dir, name), []byte("archive"))
	}

	deleted, err := ApplyResetRetention(dir, now)
	if err != nil {
		t.Fatalf("ApplyResetRetention: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := os.Stat(filepath.Join(dir, old)); !errors.Is(err, os.ErrNotExist) {
		t.Error("archive older than 30 days was kept")
	}
	if _, err := os.Stat(filepath.Join(dir, recent)); err != nil {
		t.Error("archive younger than 30 days was deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, normal)); err != nil {
		t.Error("a normal backup was deleted by reset retention")
	}
	if n, err := ApplyRetention(dir, 1); err != nil || n != 1 {
		t.Fatalf("ApplyRetention deleted %d (err %v), want 1 normal backup", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, recent)); err != nil {
		t.Error("normal retention touched a reset archive")
	}
}

// resetFixture is a seeded live database with a Handlers, as a parent would reach them.
type resetFixture struct {
	h        *Handlers
	conn     *sql.DB
	dir      string
	avatars  string
	password string
	userID   string
	memberID string
}

func newResetFixture(t *testing.T) resetFixture {
	t.Helper()
	ctx := context.Background()
	conn := openTestDB(t)
	f := resetFixture{conn: conn, password: seedDemo(t, conn)}
	root := t.TempDir()
	f.dir = filepath.Join(root, "backups")
	f.avatars = filepath.Join(root, "avatars")
	writeFile(t, filepath.Join(f.avatars, "sam.png"), []byte("sam avatar"))

	if err := conn.QueryRowContext(ctx,
		`SELECT fm.id, u.id FROM family_members fm JOIN users u ON u.id = fm.user_id WHERE u.display_name = 'Sam'`,
	).Scan(&f.memberID, &f.userID); err != nil {
		t.Fatalf("find Sam: %v", err)
	}
	f.h = NewHandlers(conn, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), f.dir, testDBKey, filepath.Join(root, "nestwall.db"), f.avatars)
	return f
}

func (f resetFixture) post(t *testing.T, body factoryResetRequest) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/reset", bytes.NewReader(raw))
	ctx := context.WithValue(req.Context(), permissions.ContextKeyUserID, f.userID)
	ctx = context.WithValue(ctx, permissions.ContextKeyFamilyMemberID, f.memberID)
	rec := httptest.NewRecorder()
	f.h.FactoryReset(rec, req.WithContext(ctx))
	return rec
}

func TestFactoryResetWrongWordChangesNothing(t *testing.T) {
	f := newResetFixture(t)
	members := sqlCount(t, f.conn, `SELECT COUNT(*) FROM family_members`)
	runs := sqlCount(t, f.conn, `SELECT COUNT(*) FROM backup_runs`)

	rec := f.post(t, factoryResetRequest{Password: f.password, Confirm: "delete everything"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := sqlCount(t, f.conn, `SELECT COUNT(*) FROM family_members`); got != members {
		t.Fatalf("family members changed: %d -> %d", members, got)
	}
	if got := sqlCount(t, f.conn, `SELECT COUNT(*) FROM backup_runs`); got != runs {
		t.Fatalf("backup history changed: %d -> %d; no backup should be taken", runs, got)
	}
}

func TestFactoryResetWrongPasswordChangesNothing(t *testing.T) {
	f := newResetFixture(t)
	members := sqlCount(t, f.conn, `SELECT COUNT(*) FROM family_members`)

	rec := f.post(t, factoryResetRequest{Password: "not-the-password", Confirm: ResetWord})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := sqlCount(t, f.conn, `SELECT COUNT(*) FROM family_members`); got != members {
		t.Fatalf("family members changed: %d -> %d", members, got)
	}
	if f.h.restoring.Load() {
		t.Fatal("reset flag left set after a rejected password")
	}
}

func TestFactoryResetWithPasswordAndWordWipes(t *testing.T) {
	f := newResetFixture(t)

	rec := f.post(t, factoryResetRequest{Password: f.password, Confirm: ResetWord})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if n := sqlCount(t, f.conn, `SELECT COUNT(*) FROM families`); n != 0 {
		t.Fatalf("families = %d after reset, want 0", n)
	}
}

func TestResetPreviewCountsSeededData(t *testing.T) {
	conn := openTestDB(t)
	seedDemo(t, conn)
	counts, err := CountForReset(context.Background(), conn)
	if err != nil {
		t.Fatalf("CountForReset: %v", err)
	}
	if counts.Members == 0 || counts.Children == 0 {
		t.Fatalf("counts = %+v, want members and children from the demo family", counts)
	}
}
