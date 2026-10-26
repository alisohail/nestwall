package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"nestwall/backend/internal/platform/web"
)

const (
	// resetPrefix names the archives a factory reset takes. Retention by count ignores them.
	resetPrefix = "nestwall-reset-"
	// resetRetention is how long a factory reset archive is kept.
	resetRetention = 30 * 24 * time.Hour
	// ResetWord is the text the parent types to confirm a factory reset.
	ResetWord = "DELETE EVERYTHING"
)

// ErrResetBackupFailed means the backup taken before the reset did not finish, so nothing was removed.
var ErrResetBackupFailed = errors.New("the backup before the reset failed")

// keptTables are reference data seeded by migrations. First-run setup needs the roles and permissions.
var keptTables = map[string]bool{
	"schema_migrations":        true,
	"roles":                    true,
	"permissions":              true,
	"role_permissions":         true,
	"role_default_permissions": true,
}

// ResetCounts is what a factory reset removes. The dialog shows it before anything is removed.
type ResetCounts struct {
	Members       int64 `json:"members"`
	Children      int64 `json:"children"`
	Tasks         int64 `json:"tasks"`
	PointsEntries int64 `json:"points_entries"`
	Events        int64 `json:"events"`
	Avatars       int64 `json:"avatars"`
	WallDevices   int64 `json:"wall_devices"`
	APITokens     int64 `json:"api_tokens"`
}

// CountForReset returns the exact counts a factory reset would remove.
func CountForReset(ctx context.Context, db *sql.DB) (ResetCounts, error) {
	var c ResetCounts
	queries := []struct {
		dst   *int64
		query string
	}{
		{&c.Members, `SELECT COUNT(*) FROM family_members`},
		{&c.Children, `SELECT COUNT(*) FROM users WHERE is_child = 1`},
		{&c.Tasks, `SELECT COUNT(*) FROM task_definitions`},
		{&c.PointsEntries, `SELECT COUNT(*) FROM points_ledger`},
		{&c.Events, `SELECT COUNT(*) FROM calendar_events`},
		{&c.Avatars, `SELECT COUNT(*) FROM member_avatars`},
		{&c.WallDevices, `SELECT COUNT(*) FROM wall_devices`},
		{&c.APITokens, `SELECT COUNT(*) FROM api_tokens`},
	}
	for _, q := range queries {
		if err := db.QueryRowContext(ctx, q.query).Scan(q.dst); err != nil {
			return ResetCounts{}, fmt.Errorf("count for reset: %w", err)
		}
	}
	return c, nil
}

// ResetOptions controls a factory reset.
type ResetOptions struct {
	Dir        string
	DBKey      string
	AvatarsDir string
	Passphrase string
	Logger     *slog.Logger
}

// FactoryReset takes a verified backup, then removes all family data in one transaction. The backup
// is kept for resetRetention. If the backup fails, nothing is removed.
func FactoryReset(ctx context.Context, db *sql.DB, opt ResetOptions) (Result, error) {
	backup, err := Run(ctx, db, Options{
		Dir:        opt.Dir,
		DBKey:      opt.DBKey,
		AvatarsDir: opt.AvatarsDir,
		Passphrase: opt.Passphrase,
		Kind:       "before_reset",
		Prefix:     resetPrefix,
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrResetBackupFailed, err)
	}

	runMu.Lock()
	defer runMu.Unlock()
	if err := wipe(ctx, db, backup.ID); err != nil {
		return backup, fmt.Errorf("remove data: %w", err)
	}
	// Orphaned avatar files are harmless, so a failure here is logged rather than reported.
	if err := os.RemoveAll(opt.AvatarsDir); err != nil && opt.Logger != nil {
		opt.Logger.Warn("factory reset could not remove avatars", slog.String("error", err.Error()))
	}
	return backup, nil
}

// wipe deletes the rows of every table except the reference data. Backup schedule and keep settings go
// back to their defaults; the encryption passphrase is kept. The reset's own backup row stays in the history.
func wipe(ctx context.Context, db *sql.DB, keepRunID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reset: %w", err)
	}
	defer tx.Rollback()

	// Deleting parents before children would need an order; deferring the checks to commit avoids it.
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("defer foreign keys: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("read table name: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("list tables: %w", err)
	}

	// Append-only tables such as points_ledger block deletes with triggers. The triggers are saved,
	// dropped for the wipe, and created again before the commit.
	triggers, err := tx.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type = 'trigger' AND sql IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("list triggers: %w", err)
	}
	type trigger struct{ name, sql string }
	var saved []trigger
	for triggers.Next() {
		var t trigger
		if err := triggers.Scan(&t.name, &t.sql); err != nil {
			triggers.Close()
			return fmt.Errorf("read trigger: %w", err)
		}
		saved = append(saved, t)
	}
	if err := triggers.Close(); err != nil {
		return fmt.Errorf("list triggers: %w", err)
	}
	for _, t := range saved {
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER "`+t.name+`"`); err != nil {
			return fmt.Errorf("drop trigger %s: %w", t.name, err)
		}
	}

	for _, name := range tables {
		if strings.HasPrefix(name, "sqlite_") || keptTables[name] || name == "backup_runs" || name == "backup_settings" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM "`+name+`"`); err != nil {
			return fmt.Errorf("clear %s: %w", name, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM backup_runs WHERE id <> ?`, keepRunID); err != nil {
		return fmt.Errorf("clear backup history: %w", err)
	}
	// The passphrase and the encrypt choice stay. The reset archive is sealed with that passphrase,
	// so dropping it would leave the archive unreadable. The schedule and keep count go to defaults.
	var encrypt int
	var passphraseSealed sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT encrypt, passphrase_sealed FROM backup_settings WHERE id = 1`).Scan(&encrypt, &passphraseSealed); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read backup settings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM backup_settings`); err != nil {
		return fmt.Errorf("clear backup settings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO backup_settings (id, encrypt, passphrase_sealed) VALUES (1, ?, ?)`, encrypt, passphraseSealed); err != nil {
		return fmt.Errorf("reset backup settings: %w", err)
	}
	for _, t := range saved {
		if _, err := tx.ExecContext(ctx, t.sql); err != nil {
			return fmt.Errorf("restore trigger %s: %w", t.name, err)
		}
	}
	return tx.Commit()
}

// ResetPreview returns the counts a factory reset would remove.
func (h *Handlers) ResetPreview(w http.ResponseWriter, r *http.Request) {
	counts, err := CountForReset(r.Context(), h.db)
	if err != nil {
		h.internalError(w, "reset: count", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, counts)
}

type factoryResetRequest struct {
	Password string `json:"password"`
	Confirm  string `json:"confirm"`
}

// FactoryReset removes all family data after the parent types the confirm word and enters their password.
// Both are checked before any backup is taken, so a wrong entry does nothing.
func (h *Handlers) FactoryReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body factoryResetRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBackupBody)).Decode(&body); err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_body", "The request could not be read", nil)
		return
	}
	userID, _, _, ok := h.caller(w, r)
	if !ok {
		return
	}
	if body.Confirm != ResetWord {
		web.RespondError(w, http.StatusBadRequest, "confirm_incorrect", "Type DELETE EVERYTHING exactly to confirm.", nil)
		return
	}
	if !h.checkPassword(w, r, userID, body.Password) {
		return
	}

	if !h.restoring.CompareAndSwap(false, true) {
		web.RespondError(w, http.StatusConflict, "restore_in_progress", "A restore or reset is already running.", nil)
		return
	}
	defer h.restoring.Store(false)

	passphrase, err := activePassphrase(ctx, h.db, h.dbKey)
	if err != nil {
		h.passphraseFailed(w, err)
		return
	}

	_, err = FactoryReset(ctx, h.db, ResetOptions{
		Dir:        h.dir,
		DBKey:      h.dbKey,
		AvatarsDir: h.avatarsDir,
		Passphrase: passphrase,
		Logger:     h.logger,
	})
	if errors.Is(err, ErrResetBackupFailed) {
		h.logger.Error("factory reset aborted", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "reset_backup_failed", "The backup before the reset failed, so nothing was removed. The reason is in the history.", nil)
		return
	}
	if err != nil {
		h.internalError(w, "reset: remove data", err)
		return
	}

	if _, err := ApplyResetRetention(h.dir, h.clock.Now()); err != nil {
		h.logger.Error("factory reset archive retention failed", slog.String("error", err.Error()))
	}
	// Server log only: the audit log is part of the data that the reset removes.
	h.logger.Warn("factory reset completed")
	web.RespondJSON(w, http.StatusOK, map[string]bool{"reset": true})
}
