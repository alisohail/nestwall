package diagnostics

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/buildinfo"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/testhelper"
)

type fixture struct {
	h   *Handlers
	db  *sql.DB
	fam testhelper.Family
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	return fixture{h: NewHandlers(db, "", slog.New(slog.NewTextHandler(io.Discard, nil))), db: db, fam: fam}
}

func (f fixture) get(t *testing.T, memberID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/diagnostics", nil)
	ctx := context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, memberID)
	ctx = context.WithValue(ctx, permissions.ContextKeyUserID, f.fam.ParentUserID)
	rec := httptest.NewRecorder()
	f.h.Get(rec, req.WithContext(ctx))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) Response {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestGetEmptyFamily(t *testing.T) {
	f := newFixture(t)
	resp := decode(t, f.get(t, f.fam.MemberID))

	if resp.Version != buildinfo.Version {
		t.Fatalf("version = %q, want %q", resp.Version, buildinfo.Version)
	}
	if resp.Migrations.Applied < 1 || resp.Migrations.Latest == "" {
		t.Fatalf("migrations = %+v, want applied migrations", resp.Migrations)
	}
	if resp.LastBackup != nil || resp.LastSuccessfulBackupAt != nil {
		t.Fatalf("backups = %+v / %v, want none", resp.LastBackup, resp.LastSuccessfulBackupAt)
	}
	if resp.Sync.LinkedCalendars != 0 || resp.Sync.Paused != 0 || resp.Sync.Failing != 0 || resp.Sync.LastSyncedAt != nil {
		t.Fatalf("sync = %+v, want zeros", resp.Sync)
	}
}

func TestGetBackupsReportLatestRunAndLastGood(t *testing.T) {
	f := newFixture(t)
	mustExec(t, f.db, `INSERT INTO backup_runs (id, kind, status, started_at, finished_at) VALUES
		('b-old', 'scheduled', 'ok', '2026-10-20 10:00:00+00:00', '2026-10-20 10:01:00+00:00'),
		('b-new', 'scheduled', 'failed', '2026-10-21 10:00:00+00:00', '2026-10-21 10:00:05+00:00')`)

	resp := decode(t, f.get(t, f.fam.MemberID))
	if resp.LastBackup == nil || resp.LastBackup.Status != "failed" || resp.LastBackup.Kind != "scheduled" {
		t.Fatalf("last_backup = %+v, want the failed run", resp.LastBackup)
	}
	if resp.LastSuccessfulBackupAt == nil || !resp.LastSuccessfulBackupAt.Equal(parse(t, "2026-10-20T10:01:00Z")) {
		t.Fatalf("last_successful_backup_at = %v, want the 20th", resp.LastSuccessfulBackupAt)
	}
}

func TestGetCountsOnlyUrlLinkedCalendarsAndRedactsThem(t *testing.T) {
	f := newFixture(t)
	const secretURL = "https://calendar.example.com/private/feed-token-123.ics"
	mustExec(t, f.db, `INSERT INTO calendar_imports
		(id, family_id, name, created_by, source_url_sealed, paused, last_sync_error, last_synced_at) VALUES
		('c-ok', ?, 'School Calendar', ?, 'sealed-a', 0, NULL, '2026-10-21 08:00:00+00:00'),
		('c-bad', ?, 'Sports Calendar', ?, 'sealed-b', 1, 'fetch_failed', '2026-10-20 08:00:00+00:00'),
		('c-file', ?, 'Uploaded File', ?, NULL, 0, NULL, NULL)`,
		f.fam.FamilyID, f.fam.ParentUserID,
		f.fam.FamilyID, f.fam.ParentUserID,
		f.fam.FamilyID, f.fam.ParentUserID)

	rec := f.get(t, f.fam.MemberID)
	resp := decode(t, rec)

	if resp.Sync.LinkedCalendars != 2 || resp.Sync.Paused != 1 || resp.Sync.Failing != 1 {
		t.Fatalf("sync = %+v, want 2 linked, 1 paused, 1 failing", resp.Sync)
	}
	if resp.Sync.LastSyncedAt == nil || !resp.Sync.LastSyncedAt.Equal(parse(t, "2026-10-21T08:00:00Z")) {
		t.Fatalf("last_synced_at = %v, want the 21st", resp.Sync.LastSyncedAt)
	}
	for _, leak := range []string{secretURL, "feed-token", "sealed-a", "School Calendar", "Sports Calendar", "Uploaded File", "fetch_failed"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("response leaks %q: %s", leak, rec.Body.String())
		}
	}
}

func TestGetRefusesCallerWithoutMembership(t *testing.T) {
	f := newFixture(t)
	rec := f.get(t, "not-a-member")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
}

func TestGetCountsEmailQueueAndActiveWallDevices(t *testing.T) {
	f := newFixture(t)
	recent := time.Now().UTC().Add(-time.Hour).Format("2006-01-02T15:04:05.000Z")
	old := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02T15:04:05.000Z")
	mustExec(t, f.db, `INSERT INTO email_outbox (id, family_id, to_address, subject, body, status, next_attempt_at, created_at, sent_at) VALUES
		('e-pending', ?, 'a@example.com', 's', 'b', 'pending', ?, ?, NULL),
		('e-failed', ?, 'a@example.com', 's', '', 'failed', ?, ?, NULL),
		('e-sent-now', ?, 'a@example.com', 's', '', 'sent', ?, ?, ?),
		('e-sent-old', ?, 'a@example.com', 's', '', 'sent', ?, ?, ?)`,
		f.fam.FamilyID, recent, recent,
		f.fam.FamilyID, recent, recent,
		f.fam.FamilyID, recent, recent, recent,
		f.fam.FamilyID, old, old, old)

	mustExec(t, f.db, `INSERT INTO wall_devices (id, family_id, name, credential_hash, exit_code_hash, created_by, paired_at, last_seen_at, revoked_at) VALUES
		('w-live', ?, 'Kitchen', ?, ?, ?, '2026-10-01 08:00:00+00:00', '2026-10-21 08:00:00+00:00', NULL),
		('w-revoked', ?, 'Old', ?, ?, ?, '2026-10-01 08:00:00+00:00', '2026-10-22 08:00:00+00:00', '2026-10-22 09:00:00+00:00'),
		('w-unpaired', ?, 'New', NULL, NULL, ?, NULL, NULL, NULL)`,
		f.fam.FamilyID, strings.Repeat("a", 64), strings.Repeat("x", 30), f.fam.ParentUserID,
		f.fam.FamilyID, strings.Repeat("b", 64), strings.Repeat("y", 30), f.fam.ParentUserID,
		f.fam.FamilyID, f.fam.ParentUserID)

	resp := decode(t, f.get(t, f.fam.MemberID))
	if resp.Email.Pending != 1 || resp.Email.Failed != 1 || resp.Email.SentLast24 != 1 {
		t.Fatalf("email = %+v, want 1 pending, 1 failed, 1 sent in 24h", resp.Email)
	}
	if resp.WallDevices.Active != 1 {
		t.Fatalf("wall active = %d, want 1 (revoked and unpaired excluded)", resp.WallDevices.Active)
	}
	if resp.WallDevices.LastSeenAt == nil || !resp.WallDevices.LastSeenAt.Equal(parse(t, "2026-10-21T08:00:00Z")) {
		t.Fatalf("wall last_seen_at = %v, want the 21st", resp.WallDevices.LastSeenAt)
	}
}

func TestGetReportsServerErrorsAndDatabaseSize(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(t.TempDir(), "nestwall.db")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.h = NewHandlers(f.db, path, slog.New(slog.NewTextHandler(io.Discard, nil)))

	before := serverErrorCount.Load()
	RecordServerError(time.Now())
	resp := decode(t, f.get(t, f.fam.MemberID))

	if resp.ServerErrors.SinceStartCount < before+1 || resp.ServerErrors.LastAt == nil {
		t.Fatalf("server_errors = %+v, want the recorded 5xx", resp.ServerErrors)
	}
	if resp.DatabaseBytes == nil || *resp.DatabaseBytes != 5 {
		t.Fatalf("database_bytes = %v, want 5", resp.DatabaseBytes)
	}
	if resp.UptimeSeconds < 0 {
		t.Fatalf("uptime_seconds = %d, want >= 0", resp.UptimeSeconds)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func parse(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return at
}
