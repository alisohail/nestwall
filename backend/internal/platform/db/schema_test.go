package db

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"testing"

	"nestwall/backend/database"
)

// TestFreshSchemaIsForeignKeyClean builds a new database from the embedded feature migrations and
// checks that the seeded rows reference existing parents.
func TestFreshSchemaIsForeignKeyClean(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(ctx, filepath.Join(t.TempDir(), "fresh.db"), "fresh-schema-passphrase")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowid, fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			t.Fatalf("scan violation: %v", err)
		}
		t.Fatalf("foreign key violation in %s row %d -> %s", table, rowid, parent)
	}
}

// TestRBACMigrationRemovesWallAndCaregiver checks migration 0009 on a fresh database: the Wall and
// Caregiver roles and the wall codes are gone, and only the Parent role holds parent-only codes.
func TestRBACMigrationRemovesWallAndCaregiver(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(ctx, filepath.Join(t.TempDir(), "rbac.db"), "rbac-migration-passphrase")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := conn.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", query, err)
		}
		return n
	}

	if n := count(`SELECT COUNT(*) FROM roles WHERE name IN ('Wall', 'Caregiver')`); n != 0 {
		t.Errorf("Wall or Caregiver roles left: %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM permissions WHERE code LIKE 'wall.%'`); n != 0 {
		t.Errorf("wall permission codes left: %d", n)
	}

	parentOnly := []string{
		"family.manage", "members.manage", "roles.manage", "settings.manage", "backup.manage",
		"backup.restore", "data.reset", "audit.view", "tasks.approve",
		"rewards.approve", "bonus.manage",
	}
	for _, code := range parentOnly {
		n := count(`SELECT COUNT(*) FROM role_permissions rp
		            JOIN permissions p ON p.id = rp.permission_id
		            WHERE p.code = ? AND rp.role_id <> 'a1b2c3d4-e5f6-7890-abcd-ef1234567890'`, code)
		if n != 0 {
			t.Errorf("%s granted to a non-parent role", code)
		}
	}
}

// TestRBACMigrationScopesAndRoleTypes checks migration 0010 on a fresh database: no own-scope codes
// remain, the base codes carry own scope for children, and the seeded roles have the right type.
func TestRBACMigrationScopesAndRoleTypes(t *testing.T) {
	ctx := context.Background()
	conn, err := Open(ctx, filepath.Join(t.TempDir(), "rbac-scope.db"), "rbac-scope-passphrase")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := conn.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", query, err)
		}
		return n
	}

	if n := count(`SELECT COUNT(*) FROM permissions WHERE code LIKE '%.own.%'`); n != 0 {
		t.Errorf("own-scope permission codes left: %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM role_permissions WHERE access_level = 'own'
	               AND permission_id NOT IN (SELECT id FROM permissions WHERE code IN
	               ('tasks.view', 'calendar.view', 'rewards.view', 'sessions.manage'))`); n != 0 {
		t.Errorf("own grants on codes other than the view codes and sessions: %d", n)
	}

	const childRole = "c3d4e5f6-a7b8-9012-cdef-123456789012"
	const parentRole = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	// rewards.view already held a base grant at 'view' for the child, and rewards.own.view was never
	// wired to a handler, so the existing base grant wins and the child's rewards access is unchanged.
	for _, code := range []string{"tasks.view", "calendar.view"} {
		n := count(`SELECT COUNT(*) FROM role_permissions rp
		            JOIN permissions p ON p.id = rp.permission_id
		            WHERE p.code = ? AND rp.role_id = ? AND rp.access_level = 'own'`, code, childRole)
		if n != 1 {
			t.Errorf("child role %s not at own scope", code)
		}
	}
	if n := count(`SELECT COUNT(*) FROM role_permissions rp
	               JOIN permissions p ON p.id = rp.permission_id
	               WHERE p.code = 'rewards.view' AND rp.role_id = ? AND rp.access_level = 'view'`, childRole); n != 1 {
		t.Errorf("child rewards.view changed from its base grant")
	}

	if n := count(`SELECT COUNT(*) FROM roles WHERE id = ? AND role_type = 'parent'`, parentRole); n != 1 {
		t.Errorf("parent role is not typed parent")
	}
	if n := count(`SELECT COUNT(*) FROM roles WHERE id = ? AND role_type = 'child'`, childRole); n != 1 {
		t.Errorf("child role is not typed child")
	}
}

// TestMigrationFilesAreOrderedFeatureSchemas checks the naming rule for migration files: a
// four-digit sequence, then a feature name, then .up.sql. The runner applies them in filename order.
func TestMigrationFilesAreOrderedFeatureSchemas(t *testing.T) {
	files, err := fs.Glob(database.Migrations, "migrations/*.up.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no migrations embedded")
	}
	pattern := regexp.MustCompile(`^migrations/(\d{4})_[a-z_]+\.up\.sql$`)
	for i, file := range files {
		m := pattern.FindStringSubmatch(file)
		if m == nil {
			t.Errorf("migration %s does not match NNNN_feature.up.sql", file)
			continue
		}
		if want := fmt.Sprintf("%04d", i+1); m[1] != want {
			t.Errorf("migration %s has sequence %s, want %s", file, m[1], want)
		}
	}
}
