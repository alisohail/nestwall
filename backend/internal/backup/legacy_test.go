package backup

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenameLegacyArchives(t *testing.T) {
	conn := openTestDB(t)
	dir := t.TempDir()
	now := time.Date(2026, 6, 30, 2, 0, 0, 0, time.UTC)

	legacy := filePrefix + now.Format(timeLayout) + legacyExtension
	orphan := filePrefix + now.AddDate(0, 0, -1).Format(timeLayout) + legacyExtension
	for _, name := range []string{legacy, orphan, "notes.nwbackup"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(
		`INSERT INTO backup_runs (id, kind, status, encrypted, started_at, file_name) VALUES ('r1', 'manual', 'ok', 0, ?, ?)`,
		now.Format(time.RFC3339), legacy,
	); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	if err := RenameLegacyArchives(context.Background(), conn, dir, slog.Default()); err != nil {
		t.Fatalf("RenameLegacyArchives: %v", err)
	}
	// Running again must do nothing and must not fail.
	if err := RenameLegacyArchives(context.Background(), conn, dir, slog.Default()); err != nil {
		t.Fatalf("second RenameLegacyArchives: %v", err)
	}

	for _, name := range []string{legacy, orphan} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone after rename", name)
		}
	}
	for _, name := range []string{
		filePrefix + now.Format(timeLayout) + Extension,
		filePrefix + now.AddDate(0, 0, -1).Format(timeLayout) + Extension,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s should exist: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.nwbackup")); err != nil {
		t.Fatalf("unrelated file must not be touched: %v", err)
	}

	var fileName string
	if err := conn.QueryRow(`SELECT file_name FROM backup_runs WHERE id = 'r1'`).Scan(&fileName); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if want := filePrefix + now.Format(timeLayout) + Extension; fileName != want {
		t.Fatalf("file_name = %q, want %q", fileName, want)
	}
}
