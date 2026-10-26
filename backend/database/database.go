// Package database holds the SQL schema migrations. They are embedded in the
// API binary, so the container does not need the files at run time.
//
// The schema is split by feature (0001_identity, 0002_permissions, ...). Before the
// first release, a feature file may be edited in place. After a release, applied
// files are never edited; a change goes in a new file with the next number.
package database

import "embed"

// Migrations contains the migrations/*.sql files. The migration runner in
// internal/platform/db applies the *.up.sql files in filename order and records
// each one in schema_migrations, so existing databases only run the new files.
//
//go:embed migrations/*.sql
var Migrations embed.FS
