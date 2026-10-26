package backup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultKeepCount is how many archives are kept when the parents have not chosen another number.
const DefaultKeepCount = 6

// Archive is one backup file and the time it was taken.
type Archive struct {
	Name string
	At   time.Time
}

// Keep returns the names of the newest count archives. Older archives are not in the result.
func Keep(archives []Archive, count int) map[string]bool {
	sorted := append([]Archive(nil), archives...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.After(sorted[j].At) })

	keep := make(map[string]bool)
	for i, a := range sorted {
		if i >= count {
			break
		}
		keep[a.Name] = true
	}
	return keep
}

// ApplyRetention deletes all but the newest keepCount archives in dir. It only touches files named
// like archives this package writes, and it leaves names it cannot parse alone.
func ApplyRetention(dir string, keepCount int) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read backup folder: %w", err)
	}

	var archives []Archive
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		at, ok := parseName(e.Name())
		if !ok {
			continue
		}
		archives = append(archives, Archive{Name: e.Name(), At: at})
	}

	keep := Keep(archives, keepCount)
	deleted := 0
	for _, a := range archives {
		if keep[a.Name] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, a.Name)); err != nil && !os.IsNotExist(err) {
			return deleted, fmt.Errorf("delete old backup: %w", err)
		}
		deleted++
	}
	return deleted, nil
}

// PruneRuns keeps the history in step with the archives on disk. Scheduled and manual runs are limited
// to the newest keepCount. Factory reset runs are not counted; they go once their archive is past
// resetRetention, as ApplyResetRetention removes the file at the same time.
func PruneRuns(ctx context.Context, db *sql.DB, keepCount int, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("prune backup history: %w", err)
	}
	defer tx.Rollback()

	cutoff := now.Add(-resetRetention).UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM backup_runs WHERE kind = 'before_reset' AND julianday(started_at) < julianday(?)`,
		cutoff); err != nil {
		return fmt.Errorf("prune reset history: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM backup_runs WHERE kind <> 'before_reset' AND id NOT IN (
		   SELECT id FROM backup_runs WHERE kind <> 'before_reset'
		   ORDER BY julianday(started_at) DESC LIMIT ?)`, keepCount); err != nil {
		return fmt.Errorf("prune backup history: %w", err)
	}
	return tx.Commit()
}

// parseName reads the time from an archive file name, such as nestwall-20260102-020000.zip.
// Names are written in UTC by Run, so they are read as UTC.
func parseName(name string) (time.Time, bool) {
	return parseNamed(name, filePrefix)
}

// parseNamed reads the time from a file name made of prefix, a UTC time stamp and Extension.
func parseNamed(name, prefix string) (time.Time, bool) {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, Extension) {
		return time.Time{}, false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, prefix), Extension)
	at, err := time.ParseInLocation(timeLayout, stamp, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// ApplyResetRetention deletes factory reset archives older than resetRetention. They are not part of
// the keep count, because a reset is rare and its archive should outlive the normal backups.
func ApplyResetRetention(dir string, now time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read backup folder: %w", err)
	}

	cutoff := now.Add(-resetRetention)
	deleted := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		at, ok := parseNamed(e.Name(), resetPrefix)
		if !ok || !at.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
			return deleted, fmt.Errorf("delete old reset backup: %w", err)
		}
		deleted++
	}
	return deleted, nil
}

