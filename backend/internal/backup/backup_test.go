package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nestwall/backend/internal/platform/db"
)

const testDBKey = "backup-test-only-key"

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), testDBKey)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func runBackup(t *testing.T, conn *sql.DB, dir, passphrase string, now time.Time) Result {
	t.Helper()
	res, err := Run(context.Background(), conn, Options{
		Dir:        dir,
		DBKey:      testDBKey,
		AvatarsDir: filepath.Join(dir, "no-avatars"),
		Passphrase: passphrase,
		Kind:       "manual",
		Now:        now,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func TestRunWritesVerifiedArchive(t *testing.T) {
	conn := openTestDB(t)
	dir := t.TempDir()
	res := runBackup(t, conn, dir, "", time.Date(2026, 1, 2, 2, 0, 0, 0, time.Local))

	if res.Encrypted {
		t.Fatal("archive without a passphrase must not be marked encrypted")
	}
	if _, err := os.Stat(filepath.Join(dir, res.FileName)); err != nil {
		t.Fatalf("archive missing: %v", err)
	}
	if err := Verify(filepath.Join(dir, res.FileName), VerifyOptions{DBKey: testDBKey}); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	var status string
	if err := conn.QueryRow(`SELECT status FROM backup_runs WHERE id = ?`, res.ID).Scan(&status); err != nil {
		t.Fatalf("read backup_runs: %v", err)
	}
	if status != "ok" {
		t.Fatalf("status = %q, want ok", status)
	}
}

func TestEncryptedArchiveNeedsPassphrase(t *testing.T) {
	conn := openTestDB(t)
	dir := t.TempDir()
	res := runBackup(t, conn, dir, "correct horse", time.Date(2026, 1, 2, 2, 0, 0, 0, time.Local))
	path := filepath.Join(dir, res.FileName)

	if !res.Encrypted {
		t.Fatal("archive with a passphrase must be marked encrypted")
	}
	if err := Verify(path, VerifyOptions{DBKey: testDBKey}); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("no passphrase: err = %v, want ErrPassphraseRequired", err)
	}
	if err := Verify(path, VerifyOptions{DBKey: testDBKey, Passphrase: "wrong"}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("wrong passphrase: err = %v, want ErrCorrupt", err)
	}
	if err := Verify(path, VerifyOptions{DBKey: testDBKey, Passphrase: "correct horse"}); err != nil {
		t.Fatalf("right passphrase: %v", err)
	}
}

func TestSnapshotNeedsDBKey(t *testing.T) {
	conn := openTestDB(t)
	dir := t.TempDir()
	res := runBackup(t, conn, dir, "", time.Date(2026, 1, 2, 2, 0, 0, 0, time.Local))

	err := Verify(filepath.Join(dir, res.FileName), VerifyOptions{DBKey: "not-the-key"})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("wrong DB_KEY: err = %v, want ErrCorrupt", err)
	}
}

func TestTamperedArchiveFails(t *testing.T) {
	conn := openTestDB(t)
	dir := t.TempDir()
	res := runBackup(t, conn, dir, "", time.Date(2026, 1, 2, 2, 0, 0, 0, time.Local))
	path := filepath.Join(dir, res.FileName)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(path, VerifyOptions{DBKey: testDBKey}); err == nil {
		t.Fatal("tampered archive verified")
	}
}

func TestKeepNewestCount(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.Local)
	var archives []Archive
	for h := 0; h < 10; h++ {
		at := now.Add(time.Duration(-h) * time.Hour)
		archives = append(archives, Archive{Name: at.Format(timeLayout), At: at})
	}

	keep := Keep(archives, 3)
	if got := countKept(archives, keep); got != 3 {
		t.Fatalf("kept %d archives, want 3", got)
	}
	for h := 0; h < 3; h++ {
		name := now.Add(time.Duration(-h) * time.Hour).Format(timeLayout)
		if !keep[name] {
			t.Fatalf("archive %d hours old must be kept", h)
		}
	}
}

func TestKeepWhenFewerThanCount(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.Local)
	archives := []Archive{{Name: "a", At: now}, {Name: "b", At: now.AddDate(-2, 0, 0)}}
	if got := countKept(archives, Keep(archives, DefaultKeepCount)); got != 2 {
		t.Fatalf("kept %d archives, want all 2", got)
	}
}

func TestApplyRetentionOnlyTouchesOwnArchives(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.Local)
	for d := 0; d < 400; d += 10 {
		name := filePrefix + now.AddDate(0, 0, -d).Format(timeLayout) + Extension
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"notes.txt", "nestwall-broken.zip"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := ApplyRetention(dir, DefaultKeepCount)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if deleted == 0 {
		t.Fatal("expected old archives to be deleted")
	}
	for _, name := range []string{"notes.txt", "nestwall-broken.zip"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s must not be touched: %v", name, err)
		}
	}
	newest := filePrefix + now.Format(timeLayout) + Extension
	if _, err := os.Stat(filepath.Join(dir, newest)); err != nil {
		t.Fatalf("newest archive must be kept: %v", err)
	}
}

func TestPruneRunsKeepsNewestCount(t *testing.T) {
	conn := openTestDB(t)
	base := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		if _, err := conn.Exec(
			`INSERT INTO backup_runs (id, kind, status, started_at) VALUES (?, 'manual', 'ok', ?)`,
			"run-"+string(rune('a'+i)), base.AddDate(0, 0, i),
		); err != nil {
			t.Fatalf("insert run: %v", err)
		}
	}

	if err := PruneRuns(context.Background(), conn, 6, base.AddDate(0, 0, 8)); err != nil {
		t.Fatalf("PruneRuns: %v", err)
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM backup_runs`).Scan(&count); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if count != 6 {
		t.Fatalf("runs after prune = %d, want 6", count)
	}
	var oldest string
	if err := conn.QueryRow(`SELECT id FROM backup_runs ORDER BY julianday(started_at) ASC LIMIT 1`).Scan(&oldest); err != nil {
		t.Fatalf("oldest run: %v", err)
	}
	if oldest != "run-c" {
		t.Fatalf("oldest kept = %s, want run-c", oldest)
	}
}

func TestPruneRunsKeepsResetRunsUntilArchiveExpires(t *testing.T) {
	conn := openTestDB(t)
	base := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	runs := []struct {
		id, kind string
		at       time.Time
	}{
		{"reset-old", "before_reset", base.AddDate(0, 0, -40)},
		{"reset-recent", "before_reset", base.AddDate(0, 0, -5)},
		{"manual-1", "manual", base},
		{"manual-2", "manual", base.AddDate(0, 0, 1)},
	}
	for _, r := range runs {
		if _, err := conn.Exec(
			`INSERT INTO backup_runs (id, kind, status, started_at) VALUES (?, ?, 'ok', ?)`,
			r.id, r.kind, r.at,
		); err != nil {
			t.Fatalf("insert run: %v", err)
		}
	}

	if err := PruneRuns(context.Background(), conn, 1, base.AddDate(0, 0, 2)); err != nil {
		t.Fatalf("PruneRuns: %v", err)
	}

	kept := map[string]bool{}
	rows, err := conn.Query(`SELECT id FROM backup_runs`)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan run: %v", err)
		}
		kept[id] = true
	}
	want := map[string]bool{"reset-recent": true, "manual-2": true}
	if len(kept) != len(want) {
		t.Fatalf("kept runs = %v, want %v", kept, want)
	}
	for id := range want {
		if !kept[id] {
			t.Fatalf("run %s was pruned, want kept (kept = %v)", id, kept)
		}
	}
}

func countKept(archives []Archive, keep map[string]bool) int {
	n := 0
	for _, a := range archives {
		if keep[a.Name] {
			n++
		}
	}
	return n
}
