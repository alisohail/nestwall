package backup

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	// Registers the SQLCipher driver under the name "sqlite3".
	_ "github.com/mutecomm/go-sqlcipher/v4"

	"nestwall/backend/internal/platform/db"
)

var (
	// ErrCorrupt means the archive is damaged, or it was encrypted with another passphrase.
	ErrCorrupt = errors.New("backup is corrupt, or the passphrase is wrong")
	// ErrPassphraseRequired means the archive is encrypted and no passphrase was given.
	ErrPassphraseRequired = errors.New("this backup is encrypted: a passphrase is required")
	// ErrNewerVersion means the archive was written by a newer NestWall, so it cannot be read.
	ErrNewerVersion = errors.New("this backup was made by a newer version of NestWall")
)

// VerifyOptions are the secrets needed to check an archive.
type VerifyOptions struct {
	// DBKey unlocks the database snapshot inside the archive.
	DBKey string
	// Passphrase decrypts the archive when it is encrypted.
	Passphrase string
}

// Verify checks an archive without restoring it: the wrapper, the manifest, the checksum and size
// of every file, the SQLite integrity check of the snapshot, and the row counts.
func Verify(path string, opt VerifyOptions) error {
	arc, err := openArchive(path, opt.Passphrase)
	if err != nil {
		return err
	}
	man, entries := arc.man, arc.entries
	if len(entries) != len(man.Files)+1 {
		return ErrCorrupt
	}

	dbFile := ""
	for _, want := range man.Files {
		f, ok := entries[want.Name]
		if !ok {
			return ErrCorrupt
		}
		content, err := readEntry(f)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		if int64(len(content)) != want.Size || hex.EncodeToString(sum[:]) != want.SHA256 {
			return ErrCorrupt
		}
		if want.Name == databaseName {
			dbFile = want.Name
		}
	}
	if dbFile == "" {
		return ErrCorrupt
	}

	staging, err := os.MkdirTemp("", "nwbackup-verify-")
	if err != nil {
		return fmt.Errorf("create verify folder: %w", err)
	}
	defer os.RemoveAll(staging)

	snapshot := filepath.Join(staging, "nestwall.db")
	content, err := readEntry(entries[databaseName])
	if err != nil {
		return err
	}
	if err := os.WriteFile(snapshot, content, 0o600); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	return checkSnapshot(snapshot, opt.DBKey, man.Counts)
}

// archive is an opened backup: its manifest and its zip entries, decrypted when needed.
type archive struct {
	man     manifest
	entries map[string]*zip.File
}

// openArchive reads and decrypts an archive and parses its manifest. It does not check the files.
func openArchive(path, passphrase string) (archive, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return archive{}, fmt.Errorf("read backup: %w", err)
	}
	if bytes.HasPrefix(data, []byte(magic)) {
		if passphrase == "" {
			return archive{}, ErrPassphraseRequired
		}
		if data, err = decrypt(data, passphrase); err != nil {
			return archive{}, err
		}
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return archive{}, ErrCorrupt
	}
	entries := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		entries[f.Name] = f
	}

	man, err := readManifest(entries[manifestName])
	if err != nil {
		return archive{}, err
	}
	return archive{man: man, entries: entries}, nil
}

func readManifest(f *zip.File) (manifest, error) {
	if f == nil {
		return manifest{}, ErrCorrupt
	}
	raw, err := readEntry(f)
	if err != nil {
		return manifest{}, err
	}
	var man manifest
	if err := json.Unmarshal(raw, &man); err != nil || man.Format != formatName {
		return manifest{}, ErrCorrupt
	}
	if err := checkVersions(man); err != nil {
		return manifest{}, err
	}
	return man, nil
}

// checkVersions refuses an archive written in a newer archive format or with a newer schema than this build knows.
func checkVersions(man manifest) error {
	if man.FormatVersion > formatVersion {
		return ErrNewerVersion
	}
	latest, err := db.LatestMigration()
	if err != nil {
		return err
	}
	if man.SchemaVersion > latest {
		return ErrNewerVersion
	}
	return nil
}

// readEntry reads a zip entry. The zip reader checks the CRC at the end, so a damaged entry fails here.
func readEntry(f *zip.File) ([]byte, error) {
	if f == nil {
		return nil, ErrCorrupt
	}
	rc, err := f.Open()
	if err != nil {
		return nil, ErrCorrupt
	}
	defer rc.Close()
	content, err := io.ReadAll(rc)
	if err != nil {
		return nil, ErrCorrupt
	}
	return content, nil
}

// checkSnapshot opens the snapshot with the database key and checks that it is intact and that
// its row counts match the manifest. It does not run migrations.
func checkSnapshot(path, key string, counts map[string]int64) error {
	dsn := path + "?_pragma_key=" + url.QueryEscape(key)
	conn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("open snapshot: %w", err)
	}
	defer conn.Close()

	var integrity string
	if err := conn.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("snapshot does not open with DB_KEY: %w", ErrCorrupt)
	}
	if integrity != "ok" {
		return ErrCorrupt
	}
	for table, want := range counts {
		var got int64
		if err := conn.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil || got != want {
			return ErrCorrupt
		}
	}
	return nil
}
