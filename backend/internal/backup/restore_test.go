package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nestwall/backend/internal/demo"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/db"
)

// openDBAt opens an encrypted database at path with the test key.
func openDBAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create folder: %v", err)
	}
	conn, err := db.Open(context.Background(), path, testDBKey)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// seedFamily fills a database with the Rivera demo family and writes an avatar beside it.
func seedFamily(t *testing.T, dbPath string) {
	t.Helper()
	conn := openDBAt(t, dbPath)
	if _, err := demo.SeedWithPassword(context.Background(), conn, clock.RealClock{}, ""); err != nil {
		t.Fatalf("seed demo family: %v", err)
	}
	writeFile(t, filepath.Join(filepath.Dir(dbPath), "avatars", "sam.png"), []byte("sam avatar"))
	writeFile(t, filepath.Join(filepath.Dir(dbPath), "avatars", "rivera", "ava.png"), []byte("ava avatar"))
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create folder: %v", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func restoreOptions(archive, dbPath, backupDir, passphrase string) RestoreOptions {
	return RestoreOptions{
		Archive:    archive,
		DBPath:     dbPath,
		DBKey:      testDBKey,
		BackupDir:  backupDir,
		AvatarsDir: filepath.Join(filepath.Dir(dbPath), "avatars"),
		Passphrase: passphrase,
	}
}

func TestRestoreRoundTripOnCleanMachine(t *testing.T) {
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "src", "nestwall.db")
	seedFamily(t, src)
	srcConn := openDBAt(t, src)
	want, err := countRows(ctx, srcConn)
	if err != nil {
		t.Fatalf("count source rows: %v", err)
	}

	backupDir := t.TempDir()
	res, err := Run(ctx, srcConn, Options{
		Dir:        backupDir,
		DBKey:      testDBKey,
		AvatarsDir: filepath.Join(filepath.Dir(src), "avatars"),
		Kind:       "manual",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// A new machine has no database and no avatars yet.
	dst := filepath.Join(t.TempDir(), "data", "nestwall.db")
	out, err := Restore(ctx, restoreOptions(filepath.Join(backupDir, res.FileName), dst, backupDir, ""))
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if out.SafetyBackup != "" {
		t.Fatalf("SafetyBackup = %q on a clean machine, want none", out.SafetyBackup)
	}

	restored := openDBAt(t, dst)
	got, err := countRows(ctx, restored)
	if err != nil {
		t.Fatalf("count restored rows: %v", err)
	}
	for table, n := range want {
		if got[table] != n {
			t.Errorf("%s: restored %d rows, want %d", table, got[table], n)
		}
	}

	avatar, err := os.ReadFile(filepath.Join(filepath.Dir(dst), "avatars", "rivera", "ava.png"))
	if err != nil || !bytes.Equal(avatar, []byte("ava avatar")) {
		t.Fatalf("restored avatar = %q, %v; want the original bytes", avatar, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "avatars"+setAside)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging leftovers remain: %v", err)
	}
}

func TestRestoreSavesCurrentDataAndReplacesAvatars(t *testing.T) {
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "src", "nestwall.db")
	seedFamily(t, src)
	srcConn := openDBAt(t, src)
	backupDir := t.TempDir()
	res, err := Run(ctx, srcConn, Options{
		Dir:        backupDir,
		DBKey:      testDBKey,
		AvatarsDir: filepath.Join(filepath.Dir(src), "avatars"),
		Kind:       "manual",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The target has its own, empty database and an avatar the archive does not have.
	dst := filepath.Join(t.TempDir(), "data", "nestwall.db")
	dstConn := openDBAt(t, dst)
	writeFile(t, filepath.Join(filepath.Dir(dst), "avatars", "old.png"), []byte("old"))
	// The API is stopped before a restore, so release the live database first.
	if err := dstConn.Close(); err != nil {
		t.Fatalf("close target database: %v", err)
	}

	out, err := Restore(ctx, restoreOptions(filepath.Join(backupDir, res.FileName), dst, backupDir, ""))
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if out.SafetyBackup == "" {
		t.Fatal("no safety backup of the current data")
	}
	if err := Verify(filepath.Join(backupDir, out.SafetyBackup), VerifyOptions{DBKey: testDBKey}); err != nil {
		t.Fatalf("safety backup does not verify: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "avatars", "old.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old avatar still present after restore: %v", err)
	}
}

func TestRestoreWrongPassphraseChangesNothing(t *testing.T) {
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "src", "nestwall.db")
	seedFamily(t, src)
	srcConn := openDBAt(t, src)
	backupDir := t.TempDir()
	res := runBackup(t, srcConn, backupDir, "correct horse", time.Date(2026, 1, 2, 2, 0, 0, 0, time.Local))
	archive := filepath.Join(backupDir, res.FileName)

	dst := filepath.Join(t.TempDir(), "data", "nestwall.db")
	openDBAt(t, dst)
	before, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	filesBefore := countFiles(t, backupDir)

	if _, err := Restore(ctx, restoreOptions(archive, dst, backupDir, "")); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("no passphrase: err = %v, want ErrPassphraseRequired", err)
	}
	if _, err := Restore(ctx, restoreOptions(archive, dst, backupDir, "wrong horse")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("wrong passphrase: err = %v, want ErrCorrupt", err)
	}

	after, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read target after failed restore: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a failed restore changed the database")
	}
	if got := countFiles(t, backupDir); got != filesBefore {
		t.Fatalf("a failed restore wrote %d new files, want none", got-filesBefore)
	}
}

func TestCheckVersionsRefusesNewerBackups(t *testing.T) {
	latest, err := db.LatestMigration()
	if err != nil || latest == "" {
		t.Fatalf("LatestMigration = %q, %v", latest, err)
	}

	cases := []struct {
		name string
		man  manifest
		want error
	}{
		{"same schema", manifest{FormatVersion: formatVersion, SchemaVersion: latest}, nil},
		{"older schema", manifest{FormatVersion: formatVersion, SchemaVersion: "000001"}, nil},
		{"newer schema", manifest{FormatVersion: formatVersion, SchemaVersion: "999999"}, ErrNewerVersion},
		{"newer format", manifest{FormatVersion: formatVersion + 1, SchemaVersion: latest}, ErrNewerVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkVersions(tc.man); !errors.Is(err, tc.want) {
				t.Fatalf("checkVersions = %v, want %v", err, tc.want)
			}
		})
	}
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	return len(entries)
}
