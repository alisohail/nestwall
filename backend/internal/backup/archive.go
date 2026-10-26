// Package backup writes and checks .zip archives of the family database.
//
// An archive is a zip file with a manifest, a snapshot of the encrypted database (taken with
// VACUUM INTO, so the snapshot is still encrypted with DB_KEY) and the avatars folder. When a
// passphrase is given, the whole zip is encrypted with AES-256-GCM. The key comes from scrypt of
// the passphrase. The passphrase is not written into archives or backup_runs. The app keeps it
// sealed with DB_KEY (see settings.go) so the scheduler can make encrypted backups unattended.
package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/scrypt"

	"nestwall/backend/internal/platform/ids"
)

const (
	// Extension is the archive file extension. Encrypted archives use it too.
	Extension = ".zip"

	filePrefix    = "nestwall-"
	formatName    = "nestwall-backup"
	formatVersion = 1
	manifestName  = "manifest.json"
	databaseName  = "database/nestwall.db"
	avatarsPrefix = "avatars/"
	timeLayout    = "20060102-150405"

	// magic marks an encrypted archive. It is followed by the scrypt salt and the GCM nonce.
	magic     = "NWBK\x01"
	saltSize  = 16
	nonceSize = 12
)

// countedTables are the tables whose row counts are stored in the manifest and rechecked on verify.
var countedTables = []string{
	"users", "families", "family_members", "member_avatars",
	"points_ledger", "task_definitions", "calendar_events",
}

// Options controls one backup run.
type Options struct {
	// Dir is the backup folder. It is created when it does not exist.
	Dir string
	// DBKey unlocks the database. Verification needs it to open the snapshot.
	DBKey string
	// AvatarsDir is the avatars folder beside the database. It may not exist yet.
	AvatarsDir string
	// Passphrase encrypts the archive when it is not empty.
	Passphrase string
	// Kind is "scheduled", "manual" or "before_reset".
	Kind string
	// Prefix names the archive file. Empty means the normal backup prefix, which retention keeps
	// by count. Reset archives use their own prefix and are kept for a fixed time instead.
	Prefix string
	// Now sets the archive time. The zero value means the current time.
	Now time.Time
}

// Result describes a successful run.
type Result struct {
	ID        string
	FileName  string
	SizeBytes int64
	Encrypted bool
	Counts    map[string]int64
}

type fileEntry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Format        string           `json:"format"`
	FormatVersion int              `json:"format_version"`
	CreatedAt     string           `json:"created_at"`
	SchemaVersion string           `json:"schema_version"`
	Counts        map[string]int64 `json:"counts"`
	Files         []fileEntry      `json:"files"`
}

// runMu stops a manual backup and the scheduler from writing at the same time.
var runMu sync.Mutex

// Run takes a backup and verifies it. A failed run is recorded in backup_runs with status failed,
// and any archive that does not verify is deleted.
func Run(ctx context.Context, conn *sql.DB, opt Options) (Result, error) {
	runMu.Lock()
	defer runMu.Unlock()

	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	id := ids.New()
	recorded := false
	// The run row is written after the snapshot, so an archive never holds its own "running" row.
	record := func() error {
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO backup_runs (id, kind, status, encrypted, started_at) VALUES (?, ?, 'running', ?, ?)`,
			id, opt.Kind, opt.Passphrase != "", now,
		); err != nil {
			return fmt.Errorf("record backup run: %w", err)
		}
		recorded = true
		return nil
	}

	res, err := build(ctx, conn, opt, now, record)
	if err == nil {
		res.ID = id
		err = Verify(filepath.Join(opt.Dir, res.FileName), VerifyOptions{DBKey: opt.DBKey, Passphrase: opt.Passphrase})
		if err != nil {
			os.Remove(filepath.Join(opt.Dir, res.FileName))
		}
	}
	if err != nil {
		if !recorded {
			if rerr := record(); rerr != nil {
				return Result{}, rerr
			}
		}
		finish(ctx, conn, id, "failed", Result{}, err)
		return Result{ID: id}, err
	}

	finish(ctx, conn, id, "ok", res, nil)
	return res, nil
}

// recordFailedRun notes a run that failed before any archive was written, such as an unreadable
// passphrase. It keeps the once-a-day schedule rule working, so the failure is not retried each minute.
func recordFailedRun(ctx context.Context, conn *sql.DB, kind string, now time.Time, runErr error) {
	id := ids.New()
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO backup_runs (id, kind, status, encrypted, started_at) VALUES (?, ?, 'running', 0, ?)`,
		id, kind, now.UTC(),
	); err != nil {
		return
	}
	finish(ctx, conn, id, "failed", Result{}, runErr)
}

// build writes the archive to opt.Dir and returns its name and size. It does not verify it.
func build(ctx context.Context, conn *sql.DB, opt Options, now time.Time, afterSnapshot func() error) (Result, error) {
	if err := os.MkdirAll(opt.Dir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create backup folder: %w", err)
	}

	staging, err := os.MkdirTemp(opt.Dir, "staging-")
	if err != nil {
		return Result{}, fmt.Errorf("create staging folder: %w", err)
	}
	defer os.RemoveAll(staging)

	snapshot := filepath.Join(staging, "nestwall.db")
	if _, err := conn.ExecContext(ctx, `VACUUM INTO ?`, snapshot); err != nil {
		return Result{}, fmt.Errorf("snapshot database: %w", err)
	}
	if err := afterSnapshot(); err != nil {
		return Result{}, err
	}

	counts, err := countRows(ctx, conn)
	if err != nil {
		return Result{}, err
	}
	schema, err := schemaVersion(ctx, conn)
	if err != nil {
		return Result{}, err
	}

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	var files []fileEntry

	entry, err := addFile(zw, databaseName, snapshot)
	if err != nil {
		return Result{}, err
	}
	files = append(files, entry)

	avatars, err := avatarFiles(opt.AvatarsDir)
	if err != nil {
		return Result{}, err
	}
	for _, rel := range avatars {
		entry, err := addFile(zw, avatarsPrefix+filepath.ToSlash(rel), filepath.Join(opt.AvatarsDir, rel))
		if err != nil {
			return Result{}, err
		}
		files = append(files, entry)
	}

	man := manifest{
		Format:        formatName,
		FormatVersion: formatVersion,
		CreatedAt:     now.Format(time.RFC3339),
		SchemaVersion: schema,
		Counts:        counts,
		Files:         files,
	}
	manBytes, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("encode manifest: %w", err)
	}
	mw, err := zw.Create(manifestName)
	if err != nil {
		return Result{}, fmt.Errorf("write manifest: %w", err)
	}
	if _, err := mw.Write(manBytes); err != nil {
		return Result{}, fmt.Errorf("write manifest: %w", err)
	}
	if err := zw.Close(); err != nil {
		return Result{}, fmt.Errorf("close archive: %w", err)
	}

	payload := zipBuf.Bytes()
	if opt.Passphrase != "" {
		payload, err = encrypt(payload, opt.Passphrase)
		if err != nil {
			return Result{}, err
		}
	}

	prefix := opt.Prefix
	if prefix == "" {
		prefix = filePrefix
	}
	name := prefix + now.Format(timeLayout) + Extension
	final := filepath.Join(opt.Dir, name)
	if _, err := os.Stat(final); err == nil {
		return Result{}, fmt.Errorf("backup %s already exists", name)
	}
	tmp := final + ".part"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return Result{}, fmt.Errorf("write archive: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return Result{}, fmt.Errorf("finish archive: %w", err)
	}

	return Result{
		FileName:  name,
		SizeBytes: int64(len(payload)),
		Encrypted: opt.Passphrase != "",
		Counts:    counts,
	}, nil
}

// addFile copies a file into the zip and returns its manifest entry.
func addFile(zw *zip.Writer, name, path string) (fileEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return fileEntry{}, fmt.Errorf("read %s: %w", name, err)
	}
	defer f.Close()

	w, err := zw.Create(name)
	if err != nil {
		return fileEntry{}, fmt.Errorf("add %s: %w", name, err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), f)
	if err != nil {
		return fileEntry{}, fmt.Errorf("add %s: %w", name, err)
	}
	return fileEntry{Name: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// avatarFiles lists the avatar files relative to dir. A missing folder means no avatars.
func avatarFiles(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("read avatars folder: %w", err)
	}
	var rels []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list avatars: %w", err)
	}
	return rels, nil
}

func countRows(ctx context.Context, conn *sql.DB) (map[string]int64, error) {
	counts := make(map[string]int64, len(countedTables))
	for _, table := range countedTables {
		var n int64
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}
		counts[table] = n
	}
	return counts, nil
}

func schemaVersion(ctx context.Context, conn *sql.DB) (string, error) {
	var v string
	if err := conn.QueryRowContext(ctx,
		`SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`,
	).Scan(&v); err != nil {
		return "", fmt.Errorf("read schema version: %w", err)
	}
	return v, nil
}

// finish sets the final status of a run. It runs with a fresh context so a cancelled request
// still records the outcome.
func finish(ctx context.Context, conn *sql.DB, id, status string, res Result, runErr error) {
	ctx = context.WithoutCancel(ctx)
	var countsJSON, errText, fileName any
	var size any
	verified := 0
	if status == "ok" {
		b, _ := json.Marshal(res.Counts)
		countsJSON = string(b)
		fileName = res.FileName
		size = res.SizeBytes
		verified = 1
	}
	if runErr != nil {
		errText = runErr.Error()
	}
	conn.ExecContext(ctx,
		`UPDATE backup_runs SET status = ?, file_name = ?, size_bytes = ?, verified = ?,
		 counts_json = ?, error_message = ?, finished_at = ? WHERE id = ?`,
		status, fileName, size, verified, countsJSON, errText, time.Now().UTC(), id,
	)
}

// encrypt returns magic || salt || nonce || ciphertext. The header is bound to the ciphertext as
// additional data, so changing it makes decryption fail.
func encrypt(plain []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, saltSize)
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("salt: %w", err)
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	gcm, err := newGCM(passphrase, salt)
	if err != nil {
		return nil, err
	}
	header := append(append([]byte(magic), salt...), nonce...)
	return append(header, gcm.Seal(nil, nonce, plain, header)...), nil
}

// decrypt reverses encrypt. A wrong passphrase and a damaged archive look the same.
func decrypt(data []byte, passphrase string) ([]byte, error) {
	hdr := len(magic) + saltSize + nonceSize
	if len(data) < hdr {
		return nil, ErrCorrupt
	}
	salt := data[len(magic) : len(magic)+saltSize]
	nonce := data[len(magic)+saltSize : hdr]
	gcm, err := newGCM(passphrase, salt)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, data[hdr:], data[:hdr])
	if err != nil {
		return nil, ErrCorrupt
	}
	return plain, nil
}

func newGCM(passphrase string, salt []byte) (cipher.AEAD, error) {
	key, err := scrypt.Key([]byte(passphrase), salt, 1<<15, 8, 1, 32)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cipher: %w", err)
	}
	return cipher.NewGCM(block)
}
