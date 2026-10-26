// Package testhelper provides reusable fixtures for integration tests. Each
// test gets its own encrypted SQLite database file in a temporary directory,
// so no external database server is needed. The package uses cgo (SQLCipher).
package testhelper

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/db"
	"nestwall/backend/internal/platform/ids"
)

// parentRoleID is the seeded Parent role from migration 000001.
const parentRoleID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

// testKey is the database passphrase used for test databases only.
const testKey = "testhelper-only-passphrase"

// Family holds the seeded data for a test fixture.
type Family struct {
	FamilyID       string       // UUID of the family
	ParentUserID   string       // UUID (pk) of the parent user
	ParentUserIDTx string       // human user_id used at login
	MemberID       string       // UUID of the parent family_member row
	SessionCookie  *http.Cookie // valid session cookie carrying the raw token
}

// OpenDB opens a fresh encrypted SQLite database in a temporary directory with
// all migrations applied. The database is closed when the test ends.
func OpenDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(context.Background(), path, testKey)
	if err != nil {
		t.Fatalf("testhelper: open database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// OneFamily seeds one family with one parent and a valid session.
// Each test has its own database file, so no cleanup is needed.
func OneFamily(t *testing.T, conn *sql.DB) Family {
	t.Helper()
	return seedFamily(t, conn, randomTag(t))
}

// seedFamily creates one family, one parent user, one family_member row and a
// valid session in a single transaction.
func seedFamily(t *testing.T, conn *sql.DB, tag string) Family {
	t.Helper()
	ctx := context.Background()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("testhelper: begin tx for %s: %v", tag, err)
	}
	defer tx.Rollback()

	familyID := ids.New()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO families (id, name, timezone, singleton_lock) VALUES (?, ?, 'UTC', NULL)`,
		familyID, fmt.Sprintf("Test Family %s", tag),
	)
	if err != nil {
		t.Fatalf("testhelper: insert family %s: %v", tag, err)
	}

	userIDTx := "tp_" + tag
	passwordHash, err := auth.HashPassword("TestPassword1!")
	if err != nil {
		t.Fatalf("testhelper: hash password: %v", err)
	}

	userUUID := ids.New()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child)
		 VALUES (?, ?, ?, ?, 0)`,
		userUUID, userIDTx, passwordHash, "Parent "+tag,
	)
	if err != nil {
		t.Fatalf("testhelper: insert user %s: %v", tag, err)
	}

	memberID := ids.New()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour)
		 VALUES (?, ?, ?, ?, 'active', '#3B82F6')`,
		memberID, familyID, userUUID, parentRoleID,
	)
	if err != nil {
		t.Fatalf("testhelper: insert family_member %s: %v", tag, err)
	}

	session, err := auth.CreateSession(ctx, tx, clock.RealClock{}, userUUID, "testhelper")
	if err != nil {
		t.Fatalf("testhelper: create session %s: %v", tag, err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("testhelper: commit %s: %v", tag, err)
	}

	return Family{
		FamilyID:       familyID,
		ParentUserID:   userUUID,
		ParentUserIDTx: userIDTx,
		MemberID:       memberID,
		SessionCookie: &http.Cookie{
			Name:  auth.SessionCookieName,
			Value: session.Token, // raw token; the database stores only its hash
		},
	}
}

// randomTag returns a short unique tag for test rows.
func randomTag(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("testhelper: random tag: %v", err)
	}
	return hex.EncodeToString(b)
}
