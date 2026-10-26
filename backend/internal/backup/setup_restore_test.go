package backup

import (
	"bytes"
	"context"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nestwall/backend/internal/platform/clock"
)

// setupRestoreRequest posts a multipart first-run restore with the archive bytes and an optional passphrase.
func setupRestoreRequest(t *testing.T, h *Handlers, data []byte, passphrase string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if passphrase != "" {
		if err := mw.WriteField("passphrase", passphrase); err != nil {
			t.Fatalf("write passphrase field: %v", err)
		}
	}
	fw, err := mw.CreateFormFile("archive", "family.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/restore", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.SetupRestore(rec, req)
	return rec
}

func TestSetupRestoreRefusesWhenFamilyExists(t *testing.T) {
	f := newRestoreFixture(t)
	data, err := os.ReadFile(filepath.Join(f.dir, f.archive))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	rec := setupRestoreRequest(t, f.h, data, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	if f.h.restoring.Load() {
		t.Fatal("restore flag left set after a refused setup restore")
	}
	if _, err := os.Stat(f.dbPath); err != nil {
		t.Fatalf("live database changed: %v", err)
	}
}

func TestSetupRestoreOnEmptyInstallReplacesData(t *testing.T) {
	ctx := context.Background()
	source := newRestoreFixture(t)
	data, err := os.ReadFile(filepath.Join(source.dir, source.archive))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}

	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "nestwall.db")
	avatars := filepath.Join(root, "data", "avatars")
	dir := filepath.Join(root, "backups")
	conn := openDBAt(t, dbPath)
	var families int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM families`).Scan(&families); err != nil {
		t.Fatalf("count families: %v", err)
	}
	if families != 0 {
		t.Fatalf("fresh database has %d families, want 0", families)
	}

	var logs bytes.Buffer
	exited := make(chan int, 1)
	h := NewHandlers(conn, clock.RealClock{}, slog.New(slog.NewTextHandler(&logs, nil)), dir, testDBKey, dbPath, avatars)
	h.exit = func(code int) { exited <- code }

	rec := setupRestoreRequest(t, h, data, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s; log %s", rec.Code, rec.Body.String(), logs.String())
	}
	select {
	case code := <-exited:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process was not asked to exit after the restore")
	}

	restored := openDBAt(t, dbPath)
	if err := restored.QueryRowContext(ctx, `SELECT COUNT(*) FROM families`).Scan(&families); err != nil {
		t.Fatalf("count restored families: %v", err)
	}
	if families == 0 {
		t.Fatal("restored database has no family")
	}
	copies, err := filepath.Glob(filepath.Join(dir, "upload-*.zip"))
	if err != nil {
		t.Fatalf("glob uploads: %v", err)
	}
	if len(copies) != 0 {
		t.Fatalf("uploaded copies left behind: %v", copies)
	}
}
