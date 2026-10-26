package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
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

// restoreFixture is a live database with the demo family, one completed backup of it, and a Handlers
// whose exit is replaced by a channel.
type restoreFixture struct {
	h        *Handlers
	conn     *sql.DB
	dbPath   string
	dir      string
	avatars  string
	archive  string
	runID    string
	password string
	userID   string
	memberID string
	exited   chan int
}

func newRestoreFixture(t *testing.T) restoreFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	f := restoreFixture{
		dbPath:  filepath.Join(root, "data", "nestwall.db"),
		avatars: filepath.Join(root, "data", "avatars"),
		dir:     filepath.Join(root, "backups"),
		exited:  make(chan int, 1),
	}
	f.conn = openDBAt(t, f.dbPath)
	seeded, err := demo.SeedWithPassword(ctx, f.conn, clock.RealClock{}, "")
	if err != nil {
		t.Fatalf("seed demo family: %v", err)
	}
	f.password = seeded.Password
	writeFile(t, filepath.Join(f.avatars, "sam.png"), []byte("sam avatar"))

	res, err := Run(ctx, f.conn, Options{Dir: f.dir, DBKey: testDBKey, AvatarsDir: f.avatars, Kind: "manual"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	f.archive = res.FileName
	f.runID = res.ID

	if err := f.conn.QueryRowContext(ctx,
		`SELECT fm.id, u.id FROM family_members fm JOIN users u ON u.id = fm.user_id WHERE u.display_name = 'Sam'`,
	).Scan(&f.memberID, &f.userID); err != nil {
		t.Fatalf("find Sam: %v", err)
	}

	f.h = NewHandlers(f.conn, clock.RealClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), f.dir, testDBKey, f.dbPath, f.avatars)
	f.h.exit = func(code int) { f.exited <- code }
	return f
}

// post sends a restore request for the fixture's backup as Sam.
func (f restoreFixture) post(t *testing.T, runID, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(downloadRequest{Password: password})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/backups/"+runID+"/restore", bytes.NewReader(body))
	req.SetPathValue("id", runID)
	ctx := context.WithValue(req.Context(), permissions.ContextKeyUserID, f.userID)
	ctx = context.WithValue(ctx, permissions.ContextKeyFamilyMemberID, f.memberID)
	rec := httptest.NewRecorder()
	f.h.Restore(rec, req.WithContext(ctx))
	return rec
}

func TestRestoreWrongPasswordChangesNothing(t *testing.T) {
	f := newRestoreFixture(t)
	rec := f.post(t, f.runID, "not-the-password")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if f.h.restoring.Load() {
		t.Fatal("restore flag left set after a rejected password")
	}
	if _, err := os.Stat(f.dbPath); err != nil {
		t.Fatalf("live database changed: %v", err)
	}
}

func TestRestoreUnknownBackupIsNotFound(t *testing.T) {
	f := newRestoreFixture(t)
	rec := f.post(t, "no-such-run", f.password)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRestoreMissingArchiveIsNotFound(t *testing.T) {
	f := newRestoreFixture(t)
	if err := os.Remove(filepath.Join(f.dir, f.archive)); err != nil {
		t.Fatalf("remove archive: %v", err)
	}
	rec := f.post(t, f.runID, f.password)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRestoreRefusesSecondRunAtOnce(t *testing.T) {
	f := newRestoreFixture(t)
	f.h.restoring.Store(true)
	rec := f.post(t, f.runID, f.password)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestRestoreReplacesDataAndAsksForRestart(t *testing.T) {
	ctx := context.Background()
	f := newRestoreFixture(t)
	// Written after the backup, so the restore must remove it.
	writeFile(t, filepath.Join(f.avatars, "stale.png"), []byte("stale"))

	rec := f.post(t, f.runID, f.password)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	var body restoreResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Restarting || body.SafetyBackup == "" {
		t.Fatalf("response = %+v, want restarting with a safety backup", body)
	}

	select {
	case code := <-f.exited:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process was not asked to exit after the restore")
	}

	if _, err := os.Stat(filepath.Join(f.avatars, "stale.png")); !os.IsNotExist(err) {
		t.Fatalf("stale avatar still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, body.SafetyBackup)); err != nil {
		t.Fatalf("safety backup archive missing: %v", err)
	}

	restored := openDBAt(t, f.dbPath)
	var safetyRows int
	if err := restored.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM backup_runs WHERE file_name = ? AND status = 'ok'`, body.SafetyBackup,
	).Scan(&safetyRows); err != nil {
		t.Fatalf("count safety rows: %v", err)
	}
	if safetyRows != 1 {
		t.Fatalf("safety backup rows in restored history = %d, want 1", safetyRows)
	}
	var restoreAudits int
	if err := restored.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action = 'backup.restored' AND actor_id = ?`, f.userID,
	).Scan(&restoreAudits); err != nil {
		t.Fatalf("count restore audit rows: %v", err)
	}
	if restoreAudits != 1 {
		t.Fatalf("backup.restored audit rows = %d, want 1", restoreAudits)
	}
}

// uploadRequest sends a restore with an uploaded copy of data as Sam.
func (f restoreFixture) uploadRequest(t *testing.T, password string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("password", password); err != nil {
		t.Fatalf("write password field: %v", err)
	}
	fw, err := mw.CreateFormFile("archive", "my-copy.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/backups/restore-upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	ctx := context.WithValue(req.Context(), permissions.ContextKeyUserID, f.userID)
	ctx = context.WithValue(ctx, permissions.ContextKeyFamilyMemberID, f.memberID)
	rec := httptest.NewRecorder()
	f.h.RestoreUpload(rec, req.WithContext(ctx))
	return rec
}

func TestRestoreUploadWrongPasswordChangesNothing(t *testing.T) {
	f := newRestoreFixture(t)
	data, err := os.ReadFile(filepath.Join(f.dir, f.archive))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	rec := f.uploadRequest(t, "not-the-password", data)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if f.h.restoring.Load() {
		t.Fatal("restore flag left set after a rejected password")
	}
}

func TestRestoreUploadReplacesDataAndRemovesCopy(t *testing.T) {
	ctx := context.Background()
	f := newRestoreFixture(t)
	data, err := os.ReadFile(filepath.Join(f.dir, f.archive))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	rec := f.uploadRequest(t, f.password, data)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	select {
	case <-f.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("process was not asked to exit after the restore")
	}

	copies, err := filepath.Glob(filepath.Join(f.dir, "upload-*.zip"))
	if err != nil {
		t.Fatalf("glob uploads: %v", err)
	}
	if len(copies) != 0 {
		t.Fatalf("uploaded copies left behind: %v", copies)
	}

	restored := openDBAt(t, f.dbPath)
	var running int
	if err := restored.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_runs WHERE status = 'running'`).Scan(&running); err != nil {
		t.Fatalf("count running rows: %v", err)
	}
	if running != 0 {
		t.Fatalf("restored history has %d running rows, want 0", running)
	}
}
