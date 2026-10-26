package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/db"
)

// setAside is appended to the live database and avatars folder while a restore swaps them.
const setAside = ".before-restore"

// RestoreOptions are the inputs of a restore.
type RestoreOptions struct {
	// Archive is the backup file to restore.
	Archive string
	// DBPath is the database file. It does not exist on a new machine.
	DBPath string
	// DBKey unlocks the database. It must be the DB_KEY the archive was made with.
	DBKey string
	// BackupDir receives the safety backup of the current database.
	BackupDir string
	// AvatarsDir is the avatars folder beside the database.
	AvatarsDir string
	// Passphrase decrypts the archive when it is encrypted.
	Passphrase string
	// Now sets the time of the safety backup. The zero value means the current time.
	Now time.Time
	// ActorID is the parent who started the restore. It is empty on first run, when no one is signed in.
	ActorID string
	// BeforeSwap runs once the restored copy is ready, just before the live files are replaced.
	// The API uses it to close its database handle. An error stops the restore.
	BeforeSwap func() error
}

// RestoreResult describes a finished restore.
type RestoreResult struct {
	// SafetyBackup is the archive taken of the current data first. It is empty when there was no database.
	SafetyBackup string
	// Counts are the row counts recorded in the restored archive.
	Counts map[string]int64
}

// Restore replaces the database and the avatars folder with the contents of an archive.
//
// The archive is verified first, then the current data is backed up, and the restored database is
// migrated forward in a staging folder. Only after all of that are the files swapped, and the swap is
// undone if it fails. Nothing changes when the archive is bad. The API must be stopped, because it
// keeps the old database open.
func Restore(ctx context.Context, opt RestoreOptions) (RestoreResult, error) {
	if err := Verify(opt.Archive, VerifyOptions{DBKey: opt.DBKey, Passphrase: opt.Passphrase}); err != nil {
		return RestoreResult{}, err
	}
	arc, err := openArchive(opt.Archive, opt.Passphrase)
	if err != nil {
		return RestoreResult{}, err
	}

	safety, err := safetyBackup(ctx, opt)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("safety backup failed, nothing was restored: %w", err)
	}

	dir := filepath.Dir(opt.DBPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return RestoreResult{}, fmt.Errorf("create database folder: %w", err)
	}
	staging, err := os.MkdirTemp(dir, "restore-")
	if err != nil {
		return RestoreResult{}, fmt.Errorf("create restore folder: %w", err)
	}
	defer os.RemoveAll(staging)

	stagedDB := filepath.Join(staging, "nestwall.db")
	if err := extractDatabase(arc, stagedDB); err != nil {
		return RestoreResult{}, err
	}
	stagedAvatars := filepath.Join(staging, "avatars")
	if err := extractAvatars(arc, stagedAvatars); err != nil {
		return RestoreResult{}, err
	}
	// Opening the staged copy applies any newer migrations, so a copy that cannot be migrated is found
	// before the live files are touched.
	if err := migrateCopy(ctx, stagedDB, opt.DBKey); err != nil {
		return RestoreResult{}, err
	}

	if opt.BeforeSwap != nil {
		if err := opt.BeforeSwap(); err != nil {
			return RestoreResult{}, fmt.Errorf("release database before restore: %w", err)
		}
	}
	if err := swap(opt, stagedDB, stagedAvatars); err != nil {
		return RestoreResult{}, err
	}
	if safety.ID != "" {
		recordSafetyRun(ctx, opt, safety)
	}
	if opt.ActorID != "" {
		recordRestoreAudit(ctx, opt, safety.FileName)
	}
	return RestoreResult{SafetyBackup: safety.FileName, Counts: arc.man.Counts}, nil
}

// recordRestoreAudit adds a backup.restored entry to the restored database. It is written after the
// swap, for the same reason as recordSafetyRun. The archive may predate the actor's account, so the row
// is written only when the actor exists there; otherwise it is skipped and the server log has the event.
func recordRestoreAudit(ctx context.Context, opt RestoreOptions, safetyFile string) {
	conn, err := db.Open(ctx, opt.DBPath, opt.DBKey)
	if err != nil {
		return
	}
	defer conn.Close()

	var familyID string
	err = conn.QueryRowContext(ctx,
		`SELECT family_id FROM family_members WHERE user_id = ? LIMIT 1`, opt.ActorID,
	).Scan(&familyID)
	if err != nil {
		return
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	meta := map[string]any{"file_name": filepath.Base(opt.Archive), "safety_backup": safetyFile}
	if err := audit.Record(ctx, tx, familyID, opt.ActorID, "backup.restored", "backup_run", nil, meta); err != nil {
		return
	}
	tx.Commit()
}

// safetyBackup archives the current database. It returns an empty Result when there is no database yet.
func safetyBackup(ctx context.Context, opt RestoreOptions) (Result, error) {
	if _, err := os.Stat(opt.DBPath); errors.Is(err, os.ErrNotExist) {
		return Result{}, nil
	} else if err != nil {
		return Result{}, fmt.Errorf("check database: %w", err)
	}

	conn, err := db.Open(ctx, opt.DBPath, opt.DBKey)
	if err != nil {
		return Result{}, fmt.Errorf("open current database: %w", err)
	}
	defer conn.Close()

	passphrase, err := activePassphrase(ctx, conn, opt.DBKey)
	if errors.Is(err, sql.ErrNoRows) {
		// A database with no backup settings (a fresh or reset install) has nothing to encrypt with.
		passphrase, err = "", nil
	}
	if err != nil {
		return Result{}, err
	}
	return Run(ctx, conn, Options{
		Dir:        opt.BackupDir,
		DBKey:      opt.DBKey,
		AvatarsDir: opt.AvatarsDir,
		Passphrase: passphrase,
		Kind:       "manual",
		Now:        opt.Now,
	})
}

// recordSafetyRun adds the safety backup to the history of the restored database. The row was written
// to the database that the swap replaced, so it would otherwise be lost. This is best effort: the
// archive is on disk either way.
func recordSafetyRun(ctx context.Context, opt RestoreOptions, safety Result) {
	conn, err := db.Open(ctx, opt.DBPath, opt.DBKey)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.ExecContext(ctx,
		`INSERT INTO backup_runs (id, kind, status, encrypted, verified, file_name, size_bytes, started_at, finished_at)
		 VALUES (?, 'manual', 'ok', ?, 1, ?, ?, ?, ?)`,
		safety.ID, safety.Encrypted, safety.FileName, safety.SizeBytes, time.Now().UTC(), time.Now().UTC(),
	)
}

func extractDatabase(arc archive, dst string) error {
	content, err := readEntry(arc.entries[databaseName])
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, content, 0o600); err != nil {
		return fmt.Errorf("write restored database: %w", err)
	}
	return nil
}

// extractAvatars writes the avatar files into dir. dir is created even when the archive has none,
// so the swap can always move it into place.
func extractAvatars(arc archive, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create avatars folder: %w", err)
	}
	for name, f := range arc.entries {
		if !strings.HasPrefix(name, avatarsPrefix) {
			continue
		}
		rel := filepath.FromSlash(strings.TrimPrefix(name, avatarsPrefix))
		if !filepath.IsLocal(rel) {
			return ErrCorrupt
		}
		content, err := readEntry(f)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("create avatars folder: %w", err)
		}
		if err := os.WriteFile(target, content, 0o600); err != nil {
			return fmt.Errorf("write avatar: %w", err)
		}
	}
	return nil
}

// migrateCopy opens the staged database, which applies any pending migrations, and closes it.
func migrateCopy(ctx context.Context, path, key string) error {
	conn, err := db.Open(ctx, path, key)
	if err != nil {
		return fmt.Errorf("restored database cannot be opened or migrated: %w", err)
	}
	return conn.Close()
}

// swap puts the staged database and avatars in place. The live files are moved aside first and
// moved back if a later step fails.
func swap(opt RestoreOptions, stagedDB, stagedAvatars string) error {
	asideDB := opt.DBPath + setAside
	hadDB, err := moveAside(opt.DBPath, asideDB)
	if err != nil {
		return fmt.Errorf("move current database aside: %w", err)
	}
	restoreDB := func() {
		os.Remove(opt.DBPath)
		if hadDB {
			os.Rename(asideDB, opt.DBPath)
		}
	}

	if err := os.Rename(stagedDB, opt.DBPath); err != nil {
		restoreDB()
		return fmt.Errorf("put restored database in place: %w", err)
	}

	asideAvatars := opt.AvatarsDir + setAside
	hadAvatars, err := moveAside(opt.AvatarsDir, asideAvatars)
	if err != nil {
		restoreDB()
		return fmt.Errorf("move current avatars aside: %w", err)
	}
	if err := os.Rename(stagedAvatars, opt.AvatarsDir); err != nil {
		if hadAvatars {
			os.Rename(asideAvatars, opt.AvatarsDir)
		}
		restoreDB()
		return fmt.Errorf("put restored avatars in place: %w", err)
	}

	// The replaced files are kept in the safety backup, so they are removed now.
	if hadDB {
		os.Remove(asideDB)
	}
	if hadAvatars {
		os.RemoveAll(asideAvatars)
	}
	return nil
}

// moveAside renames src to dst when src exists. It reports whether it moved anything.
func moveAside(src, dst string) (bool, error) {
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := os.Rename(src, dst); err != nil {
		return false, err
	}
	return true, nil
}
