// Package diagnostics serves the parent-only diagnostics page: app version, migration state,
// last backup and calendar sync health. It returns counts and times only, never file names,
// calendar names, feed links or error text from the calendar source.
package diagnostics

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"nestwall/backend/internal/buildinfo"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

// startedAt is when this process started serving, used for uptime.
var startedAt = time.Now()

// serverErrorCount and serverErrorLastNano track 5xx responses since the process started.
var (
	serverErrorCount    atomic.Int64
	serverErrorLastNano atomic.Int64
)

// RecordServerError counts a 5xx response. It is called by the request logging middleware.
func RecordServerError(at time.Time) {
	serverErrorCount.Add(1)
	serverErrorLastNano.Store(at.UnixNano())
}

// Handlers serves GET /api/v1/settings/diagnostics.
type Handlers struct {
	db     *sql.DB
	dbPath string
	logger *slog.Logger
}

// NewHandlers returns the diagnostics handlers. dbPath is the SQLite file used for the size figure; empty skips it.
func NewHandlers(db *sql.DB, dbPath string, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, dbPath: dbPath, logger: logger}
}

// Response is the diagnostics payload.
type Response struct {
	Version                string       `json:"version"`
	UptimeSeconds          int64        `json:"uptime_seconds"`
	DatabaseBytes          *int64       `json:"database_bytes"`
	Migrations             Migrations   `json:"migrations"`
	LastBackup             *BackupRun   `json:"last_backup"`
	LastSuccessfulBackupAt *time.Time   `json:"last_successful_backup_at"`
	Sync                   CalendarSync `json:"sync"`
	Email                  EmailQueue   `json:"email"`
	WallDevices            WallDevices  `json:"wall_devices"`
	ServerErrors           ServerErrors `json:"server_errors"`
}

// EmailQueue summarises the family's outgoing email. Only counts are returned.
type EmailQueue struct {
	Pending    int `json:"pending"`
	Failed     int `json:"failed"`
	SentLast24 int `json:"sent_last_24h"`
}

// WallDevices summarises paired wall displays that have not been revoked.
type WallDevices struct {
	Active     int        `json:"active"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

// ServerErrors counts 5xx responses since the process started. It is not a rolling 24-hour window.
type ServerErrors struct {
	SinceStartCount int64      `json:"since_start_count"`
	LastAt          *time.Time `json:"last_at"`
}

// Migrations describes the schema state. Migrations are applied in order, so the latest version is the highest one.
type Migrations struct {
	Applied int    `json:"applied"`
	Latest  string `json:"latest"`
}

// BackupRun is the most recent backup attempt of any kind.
type BackupRun struct {
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	FinishedAt *time.Time `json:"finished_at"`
}

// CalendarSync summarises the family's URL-linked calendars. Only counts and times are returned.
type CalendarSync struct {
	LinkedCalendars int        `json:"linked_calendars"`
	Paused          int        `json:"paused"`
	Failing         int        `json:"failing"`
	LastSyncedAt    *time.Time `json:"last_synced_at"`
}

// Get returns the diagnostics for the caller's family.
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	familyID, err := familyOf(ctx, h.db, memberID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}

	resp := Response{
		Version:       buildinfo.Version,
		UptimeSeconds: int64(time.Since(startedAt).Seconds()),
		DatabaseBytes: h.databaseBytes(),
		ServerErrors:  serverErrorsSnapshot(),
	}
	if resp.Migrations, err = loadMigrations(ctx, h.db); err != nil {
		h.internalError(w, "failed to load migrations", err)
		return
	}
	if resp.LastBackup, err = loadLastBackup(ctx, h.db); err != nil {
		h.internalError(w, "failed to load last backup", err)
		return
	}
	if resp.LastSuccessfulBackupAt, err = loadLastSuccessfulBackup(ctx, h.db); err != nil {
		h.internalError(w, "failed to load last good backup", err)
		return
	}
	if resp.Sync, err = loadCalendarSync(ctx, h.db, familyID); err != nil {
		h.internalError(w, "failed to load calendar sync", err)
		return
	}
	if resp.Email, err = loadEmailQueue(ctx, h.db, familyID); err != nil {
		h.internalError(w, "failed to load email queue", err)
		return
	}
	if resp.WallDevices, err = loadWallDevices(ctx, h.db, familyID); err != nil {
		h.internalError(w, "failed to load wall devices", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, resp)
}

// databaseBytes returns the size of the SQLite file, or nil when it is unknown.
func (h *Handlers) databaseBytes() *int64 {
	if h.dbPath == "" {
		return nil
	}
	info, err := os.Stat(h.dbPath)
	if err != nil {
		h.logger.Warn("failed to stat database", "error", err)
		return nil
	}
	size := info.Size()
	return &size
}

func serverErrorsSnapshot() ServerErrors {
	s := ServerErrors{SinceStartCount: serverErrorCount.Load()}
	if n := serverErrorLastNano.Load(); n != 0 {
		at := time.Unix(0, n)
		s.LastAt = &at
	}
	return s
}

func (h *Handlers) internalError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, "error", err)
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}

func familyOf(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, memberID string) (string, error) {
	var familyID string
	err := q.QueryRowContext(ctx,
		`SELECT family_id FROM family_members WHERE id = ? AND status = 'active'`, memberID).Scan(&familyID)
	return familyID, err
}

func loadMigrations(ctx context.Context, db *sql.DB) (Migrations, error) {
	var m Migrations
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MAX(version), '') FROM schema_migrations`,
	).Scan(&m.Applied, &m.Latest)
	return m, err
}

func loadLastBackup(ctx context.Context, db *sql.DB) (*BackupRun, error) {
	var run BackupRun
	var finished sql.NullTime
	err := db.QueryRowContext(ctx,
		`SELECT kind, status, finished_at FROM backup_runs ORDER BY julianday(started_at) DESC LIMIT 1`,
	).Scan(&run.Kind, &run.Status, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run.FinishedAt = nullTime(finished)
	return &run, nil
}

func loadLastSuccessfulBackup(ctx context.Context, db *sql.DB) (*time.Time, error) {
	var at sql.NullTime
	err := db.QueryRowContext(ctx,
		`SELECT finished_at FROM backup_runs WHERE status = 'ok' ORDER BY julianday(finished_at) DESC LIMIT 1`,
	).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return nullTime(at), nil
}

func loadCalendarSync(ctx context.Context, db *sql.DB, familyID string) (CalendarSync, error) {
	var s CalendarSync
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COALESCE(SUM(paused), 0),
		        COALESCE(SUM(CASE WHEN last_sync_error IS NOT NULL THEN 1 ELSE 0 END), 0)
		   FROM calendar_imports
		  WHERE family_id = ? AND source_url_sealed IS NOT NULL`,
		familyID,
	).Scan(&s.LinkedCalendars, &s.Paused, &s.Failing)
	if err != nil {
		return s, err
	}
	var last sql.NullTime
	err = db.QueryRowContext(ctx,
		`SELECT last_synced_at FROM calendar_imports
		  WHERE family_id = ? AND source_url_sealed IS NOT NULL AND last_synced_at IS NOT NULL
		  ORDER BY julianday(last_synced_at) DESC LIMIT 1`,
		familyID,
	).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return s, err
	}
	s.LastSyncedAt = nullTime(last)
	return s, nil
}

func loadEmailQueue(ctx context.Context, db *sql.DB, familyID string) (EmailQueue, error) {
	var e EmailQueue
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status = 'sent' AND sent_at IS NOT NULL
		                          AND julianday(sent_at) >= julianday('now', '-1 day') THEN 1 ELSE 0 END), 0)
		   FROM email_outbox
		  WHERE family_id = ?`,
		familyID,
	).Scan(&e.Pending, &e.Failed, &e.SentLast24)
	return e, err
}

func loadWallDevices(ctx context.Context, db *sql.DB, familyID string) (WallDevices, error) {
	var w WallDevices
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wall_devices WHERE family_id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`,
		familyID,
	).Scan(&w.Active)
	if err != nil {
		return w, err
	}
	var seen sql.NullTime
	err = db.QueryRowContext(ctx,
		`SELECT last_seen_at FROM wall_devices
		  WHERE family_id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL AND last_seen_at IS NOT NULL
		  ORDER BY julianday(last_seen_at) DESC LIMIT 1`,
		familyID,
	).Scan(&seen)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return w, err
	}
	w.LastSeenAt = nullTime(seen)
	return w, nil
}

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
