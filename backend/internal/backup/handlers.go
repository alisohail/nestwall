package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

const (
	maxBackupBody  = 4 << 10
	maxUploadBytes = 2 << 30
)

// Handlers serves the Data and backups settings. Every route is wrapped with settings.manage, so only
// parents reach them. Downloads and restores also need the parent's password again.
type Handlers struct {
	db         *sql.DB
	clock      clock.Clock
	logger     *slog.Logger
	dir        string
	dbKey      string
	dbPath     string
	avatarsDir string
	// restoring is set while a restore runs, so a second request cannot start another.
	restoring atomic.Bool
	// exit ends the process after a restore. Docker restarts the API on the restored data.
	exit func(int)
}

// NewHandlers returns the backup settings handlers. dir is where archives are kept, and dbPath and
// avatarsDir are the live database and avatars folder that a restore replaces.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger, dir, dbKey, dbPath, avatarsDir string) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger, dir: dir, dbKey: dbKey, dbPath: dbPath, avatarsDir: avatarsDir, exit: os.Exit}
}

// runItem is one row of the backup history. The file name stays on the server.
type runItem struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Status       string    `json:"status"`
	Verified     bool      `json:"verified"`
	Encrypted    bool      `json:"encrypted"`
	SizeBytes    int64     `json:"size_bytes"`
	ErrorMessage string    `json:"error_message,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	Downloadable bool      `json:"downloadable"`
	FileName     string    `json:"-"`
}

type statusResponse struct {
	Location      string    `json:"location"`
	LastGood      *runItem  `json:"last_good,omitempty"`
	NextRunAt     time.Time `json:"next_run_at"`
	ScheduleHour  int       `json:"schedule_hour"`
	KeepCount     int       `json:"keep_count"`
	Encrypt       bool      `json:"encrypt"`
	PassphraseSet bool      `json:"passphrase_set"`
	History       []runItem `json:"history"`
}

// GetStatus returns the schedule, the last good backup, the encryption setting and the history.
func (h *Handlers) GetStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := ReadSettings(ctx, h.db)
	if err != nil {
		h.internalError(w, "backup: load settings", err)
		return
	}

	history, err := h.queryRuns(ctx,
		`SELECT id, kind, status, verified, encrypted, COALESCE(file_name, ''), COALESCE(size_bytes, 0),
		        COALESCE(error_message, ''), started_at
		 FROM backup_runs ORDER BY julianday(started_at) DESC LIMIT ?`, settings.KeepCount)
	if err != nil {
		h.internalError(w, "backup: load history", err)
		return
	}

	lastGood, err := h.queryRuns(ctx,
		`SELECT id, kind, status, verified, encrypted, COALESCE(file_name, ''), COALESCE(size_bytes, 0),
		        COALESCE(error_message, ''), started_at
		 FROM backup_runs WHERE status = 'ok' ORDER BY julianday(finished_at) DESC LIMIT 1`)
	if err != nil {
		h.internalError(w, "backup: load last good", err)
		return
	}

	resp := statusResponse{
		Location:      h.dir,
		NextRunAt:     NextRun(h.clock.Now(), settings.ScheduleHour),
		ScheduleHour:  settings.ScheduleHour,
		KeepCount:     settings.KeepCount,
		Encrypt:       settings.Encrypt,
		PassphraseSet: settings.PassphraseSet,
		History:       history,
	}
	if len(lastGood) > 0 {
		resp.LastGood = &lastGood[0]
	}
	web.RespondJSON(w, http.StatusOK, resp)
}

// queryRuns loads backup_runs rows and marks which archives are still on disk.
func (h *Handlers) queryRuns(ctx context.Context, query string, args ...any) ([]runItem, error) {
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []runItem{}
	for rows.Next() {
		var it runItem
		if err := rows.Scan(&it.ID, &it.Kind, &it.Status, &it.Verified, &it.Encrypted,
			&it.FileName, &it.SizeBytes, &it.ErrorMessage, &it.StartedAt); err != nil {
			return nil, err
		}
		if it.Status == "ok" && it.FileName != "" {
			_, statErr := os.Stat(filepath.Join(h.dir, filepath.Base(it.FileName)))
			it.Downloadable = statErr == nil
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

type encryptionRequest struct {
	Encrypt bool `json:"encrypt"`
	// Passphrase nil keeps the stored one, "" clears it, anything else replaces it.
	Passphrase *string `json:"passphrase"`
}

// UpdateEncryption turns archive encryption on or off, and sets or clears the passphrase.
func (h *Handlers) UpdateEncryption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body encryptionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBackupBody)).Decode(&body); err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_body", "The request could not be read", nil)
		return
	}
	userID, _, familyID, ok := h.caller(w, r)
	if !ok {
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "backup: begin transaction", err)
		return
	}
	defer tx.Rollback()

	if err := SaveEncryption(ctx, tx, h.dbKey, body.Encrypt, body.Passphrase, h.clock.Now()); err != nil {
		switch {
		case errors.Is(err, ErrPassphraseNotSet):
			web.RespondError(w, http.StatusUnprocessableEntity, "passphrase_required", "Add a passphrase before turning on encryption.", nil)
		case errors.Is(err, ErrPassphraseTooShort):
			web.RespondError(w, http.StatusUnprocessableEntity, "passphrase_too_short", "The passphrase must be at least 8 characters.", nil)
		default:
			h.internalError(w, "backup: save encryption", err)
		}
		return
	}

	meta := map[string]any{"encrypt": body.Encrypt, "passphrase_changed": body.Passphrase != nil}
	if err := audit.Record(ctx, tx, familyID, userID, "backup.encryption_updated", "backup_settings", nil, meta); err != nil {
		h.internalError(w, "backup: audit encryption", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "backup: commit encryption", err)
		return
	}
	h.GetStatus(w, r)
}

// scheduleRequest is the daily backup hour and the keep count.
type scheduleRequest struct {
	ScheduleHour int `json:"schedule_hour"`
	KeepCount    int `json:"keep_count"`
}

// UpdateSchedule saves the daily backup hour and the keep count. Out-of-range values return 422.
func (h *Handlers) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body scheduleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBackupBody)).Decode(&body); err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_body", "The request could not be read", nil)
		return
	}
	userID, _, familyID, ok := h.caller(w, r)
	if !ok {
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "backup: begin transaction", err)
		return
	}
	defer tx.Rollback()

	if err := SaveSchedule(ctx, tx, body.ScheduleHour, body.KeepCount, h.clock.Now()); err != nil {
		if errors.Is(err, ErrScheduleInvalid) {
			web.RespondError(w, http.StatusUnprocessableEntity, "invalid_schedule",
				"Choose an hour from 0 to 23, and keep between 1 and 60 backups.", nil)
			return
		}
		h.internalError(w, "backup: save schedule", err)
		return
	}

	meta := map[string]any{
		"schedule_hour": body.ScheduleHour,
		"keep_count":    body.KeepCount,
	}
	if err := audit.Record(ctx, tx, familyID, userID, "backup.schedule_updated", "backup_settings", nil, meta); err != nil {
		h.internalError(w, "backup: audit schedule", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "backup: commit schedule", err)
		return
	}
	h.GetStatus(w, r)
}

// RunNow takes a manual backup straight away and applies retention after it.
func (h *Handlers) RunNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, _, familyID, ok := h.caller(w, r)
	if !ok {
		return
	}

	passphrase, err := activePassphrase(ctx, h.db, h.dbKey)
	if err != nil {
		h.passphraseFailed(w, err)
		return
	}

	now := h.clock.Now()
	res, err := Run(ctx, h.db, Options{
		Dir:        h.dir,
		DBKey:      h.dbKey,
		AvatarsDir: h.avatarsDir,
		Passphrase: passphrase,
		Kind:       "manual",
		Now:        now,
	})
	if err != nil {
		h.logger.Error("manual backup failed", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "backup_failed", "The backup failed and no archive was kept. The reason is in the history.", nil)
		return
	}
	if settings, err := ReadSettings(ctx, h.db); err != nil {
		h.logger.Error("backup retention settings failed", slog.String("error", err.Error()))
	} else {
		if _, err := ApplyRetention(h.dir, settings.KeepCount); err != nil {
			h.logger.Error("backup retention failed", slog.String("error", err.Error()))
		}
		if err := PruneRuns(ctx, h.db, settings.KeepCount, now); err != nil {
			h.logger.Error("backup history prune failed", slog.String("error", err.Error()))
		}
	}

	h.recordAudit(ctx, familyID, userID, "backup.created", res.ID, map[string]any{
		"kind": "manual", "encrypted": res.Encrypted, "size_bytes": res.SizeBytes,
	})
	h.GetStatus(w, r)
}

type downloadRequest struct {
	Password string `json:"password"`
}

// Download streams one verified archive after the parent confirms their password. The download is
// audited before any bytes are sent.
func (h *Handlers) Download(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body downloadRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBackupBody)).Decode(&body); err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_body", "The request could not be read", nil)
		return
	}
	userID, _, familyID, ok := h.caller(w, r)
	if !ok {
		return
	}

	if !h.checkPassword(w, r, userID, body.Password) {
		return
	}

	id := r.PathValue("id")
	fileName, size, err := h.lookupArchive(ctx, id)
	if errors.Is(err, errBackupNotFound) {
		web.RespondError(w, http.StatusNotFound, "backup_not_found", "That backup was not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "backup: load run", err)
		return
	}

	f, err := os.Open(filepath.Join(h.dir, filepath.Base(fileName)))
	if errors.Is(err, fs.ErrNotExist) {
		web.RespondError(w, http.StatusNotFound, "backup_missing", "That archive is no longer on disk. It may have been removed by retention.", nil)
		return
	}
	if err != nil {
		h.internalError(w, "backup: open archive", err)
		return
	}
	defer f.Close()

	h.recordAudit(ctx, familyID, userID, "backup.downloaded", id, map[string]any{"file_name": fileName})

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(fileName)+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		h.logger.Warn("backup download interrupted", slog.String("error", err.Error()))
	}
}

var errBackupNotFound = errors.New("backup not found")

// lookupArchive returns the file name and size of a backup that completed. It returns errBackupNotFound
// when there is none.
func (h *Handlers) lookupArchive(ctx context.Context, id string) (string, int64, error) {
	var fileName string
	var size int64
	err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(file_name, ''), COALESCE(size_bytes, 0) FROM backup_runs WHERE id = ? AND status = 'ok'`, id,
	).Scan(&fileName, &size)
	if errors.Is(err, sql.ErrNoRows) || fileName == "" {
		return "", 0, errBackupNotFound
	}
	return fileName, size, err
}

// checkPassword reports whether password is the signed-in parent's own. It writes the error response
// and returns false when it is not.
func (h *Handlers) checkPassword(w http.ResponseWriter, r *http.Request, userID, password string) bool {
	var hash string
	if err := h.db.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		h.internalError(w, "backup: load password hash", err)
		return false
	}
	// 403, not 401: the web client treats 401 as an expired session and signs the parent out.
	if password == "" || !auth.VerifyPassword(password, hash) {
		web.RespondError(w, http.StatusForbidden, "password_incorrect", "That password is not right.", nil)
		return false
	}
	return true
}

// restoreResponse is sent once the data has been replaced and the API is about to restart.
type restoreResponse struct {
	SafetyBackup string `json:"safety_backup"`
	Restarting   bool   `json:"restarting"`
}

// restoreExitDelay gives the response time to reach the browser before the process exits.
const restoreExitDelay = time.Second

// Restore replaces the database and avatars with one archive, then ends the process so Docker restarts
// the API on the restored data. The live database is closed just before the files are swapped. If the
// swap fails after that, the process still exits, because the database handle is already closed.
func (h *Handlers) Restore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body downloadRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBackupBody)).Decode(&body); err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_body", "The request could not be read", nil)
		return
	}
	userID, _, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if !h.checkPassword(w, r, userID, body.Password) {
		return
	}

	fileName, _, err := h.lookupArchive(ctx, r.PathValue("id"))
	if errors.Is(err, errBackupNotFound) {
		web.RespondError(w, http.StatusNotFound, "backup_not_found", "That backup was not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "backup: load run", err)
		return
	}
	archive := filepath.Join(h.dir, filepath.Base(fileName))
	if _, err := os.Stat(archive); errors.Is(err, fs.ErrNotExist) {
		web.RespondError(w, http.StatusNotFound, "backup_missing", "That archive is no longer on disk. It may have been removed by retention.", nil)
		return
	}
	passphrase, err := activePassphrase(ctx, h.db, h.dbKey)
	if err != nil {
		h.internalError(w, "backup: load passphrase", err)
		return
	}
	h.runRestore(w, r, archive, fileName, passphrase, userID)
}

// RestoreUpload restores from a zip the parent provides. The copy is saved in the backup folder, checked
// the same way as a backup from the list, and removed once the restore has finished.
func (h *Handlers) RestoreUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_body", "The file could not be read. Check that it is under the size limit.", nil)
		return
	}
	defer r.MultipartForm.RemoveAll()
	userID, _, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if !h.checkPassword(w, r, userID, r.FormValue("password")) {
		return
	}
	path, name, ok := h.saveUpload(w, r)
	if !ok {
		return
	}
	defer os.Remove(path)
	passphrase, err := activePassphrase(r.Context(), h.db, h.dbKey)
	if err != nil {
		h.internalError(w, "backup: load passphrase", err)
		return
	}
	h.runRestore(w, r, path, name, passphrase, userID)
}

// SetupRestore restores a backup on a fresh install, before any parent exists, so the first-run wizard
// can offer it. It is open without a sign-in only while no family exists; once setup is complete it
// refuses. The archive passphrase comes from the form, because a fresh install has no saved one.
func (h *Handlers) SetupRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var families int
	if err := h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM families`).Scan(&families); err != nil {
		h.internalError(w, "backup: setup restore count families", err)
		return
	}
	if families > 0 {
		web.RespondError(w, http.StatusConflict, "setup_complete", "Setup is already complete", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_body", "The file could not be read. Check that it is under the size limit.", nil)
		return
	}
	defer r.MultipartForm.RemoveAll()
	path, name, ok := h.saveUpload(w, r)
	if !ok {
		return
	}
	defer os.Remove(path)
	h.runRestore(w, r, path, name, r.FormValue("passphrase"), "")
}

// saveUpload copies the "archive" file of a multipart form into the backup folder. On success the
// caller must remove the returned path.
func (h *Handlers) saveUpload(w http.ResponseWriter, r *http.Request) (path, name string, ok bool) {
	src, header, err := r.FormFile("archive")
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "archive_missing", "Choose a backup file to restore", nil)
		return "", "", false
	}
	defer src.Close()

	dst, err := os.CreateTemp(h.dir, "upload-*.zip")
	if err != nil {
		if mkErr := os.MkdirAll(h.dir, 0o700); mkErr == nil {
			dst, err = os.CreateTemp(h.dir, "upload-*.zip")
		}
	}
	if err != nil {
		h.internalError(w, "backup: create upload", err)
		return "", "", false
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(dst.Name())
		h.internalError(w, "backup: save upload", err)
		return "", "", false
	}
	if err := dst.Close(); err != nil {
		os.Remove(dst.Name())
		h.internalError(w, "backup: save upload", err)
		return "", "", false
	}
	return dst.Name(), header.Filename, true
}

// runRestore replaces the data with the archive at path. The caller must have checked the password
// (or, on first run, that no family exists) and must supply the archive passphrase.
func (h *Handlers) runRestore(w http.ResponseWriter, r *http.Request, archive, name, passphrase, actorID string) {
	ctx := r.Context()
	if !h.restoring.CompareAndSwap(false, true) {
		web.RespondError(w, http.StatusConflict, "restore_in_progress", "A restore is already running", nil)
		return
	}
	released := false
	res, err := Restore(ctx, RestoreOptions{
		Archive:    archive,
		DBPath:     h.dbPath,
		DBKey:      h.dbKey,
		BackupDir:  h.dir,
		AvatarsDir: h.avatarsDir,
		Passphrase: passphrase,
		ActorID:    actorID,
		BeforeSwap: func() error {
			released = true
			return h.db.Close()
		},
	})
	if err != nil {
		if released {
			h.exitSoon()
		} else {
			h.restoring.Store(false)
		}
		h.restoreError(w, err)
		return
	}

	h.logger.Info("backup restored, restarting", slog.String("file_name", name))
	h.exitSoon()
	web.RespondJSON(w, http.StatusAccepted, restoreResponse{SafetyBackup: res.SafetyBackup, Restarting: true})
}

// exitSoon ends the process after the response has been sent.
func (h *Handlers) exitSoon() {
	time.AfterFunc(restoreExitDelay, func() { h.exit(0) })
}

// restoreError writes the response for a failed restore. Problems with the archive are reported to the
// parent; anything else is logged and hidden.
func (h *Handlers) restoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCorrupt):
		web.RespondError(w, http.StatusUnprocessableEntity, "restore_unreadable", "This backup could not be opened. It may be corrupt, or the passphrase may be wrong.", nil)
	case errors.Is(err, ErrPassphraseRequired):
		web.RespondError(w, http.StatusUnprocessableEntity, "passphrase_required", "This backup is encrypted. Turn on encryption with its passphrase first.", nil)
	case errors.Is(err, ErrNewerVersion):
		web.RespondError(w, http.StatusUnprocessableEntity, "backup_newer_version", "This backup was made by a newer version of NestWall.", nil)
	default:
		h.internalError(w, "backup: restore", err)
	}
}

// caller returns the signed-in user, their member id and their family. It writes the error response
// and returns false when the caller cannot be identified.
func (h *Handlers) caller(w http.ResponseWriter, r *http.Request) (userID, memberID, familyID string, ok bool) {
	ctx := r.Context()
	memberID, okMember := permissions.GetFamilyMemberID(ctx)
	userID, okUser := permissions.GetUserID(ctx)
	if !okMember || !okUser {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return "", "", "", false
	}
	err := h.db.QueryRowContext(ctx, `SELECT family_id FROM family_members WHERE id = ?`, memberID).Scan(&familyID)
	if err != nil {
		h.internalError(w, "backup: family lookup", err)
		return "", "", "", false
	}
	return userID, memberID, familyID, true
}

// recordAudit writes an audit entry for a backup that already happened. A failure is logged, not
// returned, because the backup itself succeeded.
func (h *Handlers) recordAudit(ctx context.Context, familyID, userID, action, runID string, meta map[string]any) {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.logger.Error("backup audit: begin", slog.String("error", err.Error()))
		return
	}
	defer tx.Rollback()
	target := runID
	if err := audit.Record(ctx, tx, familyID, userID, action, "backup_run", &target, meta); err != nil {
		h.logger.Error("backup audit: record", slog.String("error", err.Error()))
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.Error("backup audit: commit", slog.String("error", err.Error()))
	}
}

func (h *Handlers) passphraseFailed(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrPassphraseNotSet) {
		web.RespondError(w, http.StatusUnprocessableEntity, "passphrase_required", "Add a passphrase before making an encrypted backup.", nil)
		return
	}
	web.RespondError(w, http.StatusInternalServerError, "backup_passphrase_unreadable", "The stored backup passphrase cannot be read. Set it again in Settings.", nil)
}

func (h *Handlers) internalError(w http.ResponseWriter, what string, err error) {
	h.logger.Error(what, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}
