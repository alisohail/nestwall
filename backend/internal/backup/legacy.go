package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// legacyExtension is the extension archives had before they were renamed to .zip.
const legacyExtension = ".nwbackup"

// RenameLegacyArchives renames archives written with the old .nwbackup extension to .zip. It
// renames the files in dir and updates backup_runs.file_name to match, so downloads and history
// still work. It also renames stray files that match the archive naming but are not in
// backup_runs, so retention can prune them. It is safe to run on every start: once renamed, a
// file is no longer matched.
func RenameLegacyArchives(ctx context.Context, db *sql.DB, dir string, log *slog.Logger) error {
	rows, err := db.QueryContext(ctx,
		`SELECT id, file_name FROM backup_runs WHERE file_name LIKE '%' || ?`, legacyExtension)
	if err != nil {
		return fmt.Errorf("list legacy archives: %w", err)
	}
	type run struct{ id, name string }
	var runs []run
	for rows.Next() {
		var r run
		if err := rows.Scan(&r.id, &r.name); err != nil {
			rows.Close()
			return fmt.Errorf("scan legacy archive: %w", err)
		}
		runs = append(runs, r)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("list legacy archives: %w", err)
	}

	for _, r := range runs {
		oldName := filepath.Base(r.name)
		newName := strings.TrimSuffix(oldName, legacyExtension) + Extension
		if err := renameArchive(dir, oldName, newName); err != nil {
			log.Error("backup rename skipped", slog.String("file", oldName), slog.String("error", err.Error()))
			continue
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE backup_runs SET file_name = ? WHERE id = ?`, newName, r.id); err != nil {
			return fmt.Errorf("update backup_runs: %w", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read backup folder: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, legacyExtension) {
			continue
		}
		newName := strings.TrimSuffix(name, legacyExtension) + Extension
		if err := renameArchive(dir, name, newName); err != nil {
			log.Error("backup rename skipped", slog.String("file", name), slog.String("error", err.Error()))
		}
	}
	return nil
}

// renameArchive moves from to to inside dir. A missing source is not an error, because the
// file may already have been removed by retention. An existing target is not overwritten.
func renameArchive(dir, from, to string) error {
	if _, err := os.Stat(filepath.Join(dir, to)); err == nil {
		return fmt.Errorf("%s already exists", to)
	}
	if err := os.Rename(filepath.Join(dir, from), filepath.Join(dir, to)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
