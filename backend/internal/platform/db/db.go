// Package db opens the encrypted SQLite database and applies schema migrations.
//
// The database is one SQLCipher file. Its contents are encrypted with the key
// from DB_KEY, so the file cannot be read without that key.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"strings"

	"nestwall/backend/database"

	// Registers the SQLCipher driver under the name "sqlite3".
	_ "github.com/mutecomm/go-sqlcipher/v4"
)

const driverName = "sqlite3"

// Open opens the encrypted database at filePath, checks that key unlocks it,
// and applies pending migrations. The file is created when it does not exist.
// The returned pool is safe for concurrent use.
func Open(ctx context.Context, filePath, key string) (*sql.DB, error) {
	if key == "" {
		return nil, errors.New("database key is empty: set DB_KEY")
	}

	conn, err := sql.Open(driverName, dsn(filePath, key))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	conn.SetMaxIdleConns(4)

	// SQLCipher only detects a wrong key when it reads the file, so run a query.
	var objects int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&objects); err != nil {
		conn.Close()
		return nil, fmt.Errorf("unlock database (check DB_PATH and DB_KEY): %w", err)
	}

	if err := migrate(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// dsn builds the connection string. The key is URL-encoded so that any
// characters in the passphrase are passed to SQLCipher unchanged.
//
// _txlock=immediate makes every write transaction take the database write
// lock up front, so concurrent writers wait instead of failing.
func dsn(filePath, key string) string {
	params := url.Values{}
	params.Set("_pragma_key", key)
	params.Set("_foreign_keys", "1")
	params.Set("_busy_timeout", "5000")
	params.Set("_txlock", "immediate")
	return filePath + "?" + params.Encode()
}

// migrate applies every embedded *.up.sql file that is not yet recorded in
// schema_migrations. Each file runs in its own transaction, so a failed
// migration leaves the database unchanged.
func migrate(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	files, err := fs.Glob(database.Migrations, "migrations/*.up.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}

	for _, file := range files {
		version := strings.TrimSuffix(path.Base(file), ".up.sql")

		var applied int
		if err := conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if applied > 0 {
			continue
		}

		if err := applyMigration(ctx, conn, file, version); err != nil {
			return err
		}
	}
	return nil
}

// LatestMigration returns the version of the newest embedded migration. A backup whose schema is
// newer than this cannot be read by this build.
func LatestMigration() (string, error) {
	files, err := fs.Glob(database.Migrations, "migrations/*.up.sql")
	if err != nil {
		return "", fmt.Errorf("list migrations: %w", err)
	}
	latest := ""
	for _, file := range files {
		if v := strings.TrimSuffix(path.Base(file), ".up.sql"); v > latest {
			latest = v
		}
	}
	return latest, nil
}

// applyMigration runs one migration file and records it, in one transaction.
func applyMigration(ctx context.Context, conn *sql.DB, file, version string) error {
	body, err := fs.ReadFile(database.Migrations, file)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", version, err)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", version, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("apply migration %s: %w", version, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version) VALUES (?)`, version,
	); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}
	return nil
}
