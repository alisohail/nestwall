package backup

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/hkdf"
)

// MinPassphraseLength is the shortest archive passphrase a parent may set.
const MinPassphraseLength = 8

// The archive passphrase is stored sealed with AES-256-GCM. The key is derived from DB_KEY with its
// own info string, so it cannot be confused with other sealed secrets.
const (
	passphraseKeyInfo = "nestwall backup passphrase v1"
	passphraseAAD     = "backup-passphrase"
)

var (
	// ErrPassphraseNotSet means encryption is on but no passphrase is stored.
	ErrPassphraseNotSet = errors.New("a passphrase is needed to encrypt backups")
	// ErrPassphraseTooShort means the new passphrase is under MinPassphraseLength.
	ErrPassphraseTooShort = errors.New("passphrase is too short")
	// errPassphraseUnreadable means the stored passphrase cannot be opened with the current DB_KEY.
	errPassphraseUnreadable = errors.New("stored backup passphrase cannot be read")
)

// Hour limits for the daily backup and bounds for the keep count. They match the CHECK constraints
// in migration 000055.
const (
	MinScheduleHour = 0
	MaxScheduleHour = 23
	MinKeepCount    = 1
	MaxKeepCount    = 60
)

// ErrScheduleInvalid means the schedule hour or the keep count is outside its allowed range.
var ErrScheduleInvalid = errors.New("schedule or keep count is out of range")

// Settings is what the Data and backups page shows. The passphrase itself is never part of it.
type Settings struct {
	Encrypt       bool
	PassphraseSet bool
	ScheduleHour  int
	KeepCount     int
}

// ReadSettings returns the encryption setting, whether a passphrase is stored, the daily backup hour
// and the keep count.
func ReadSettings(ctx context.Context, db *sql.DB) (Settings, error) {
	var encrypt, hour, keep int
	var sealed sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT encrypt, passphrase_sealed, schedule_hour, keep_count
		 FROM backup_settings WHERE id = 1`,
	).Scan(&encrypt, &sealed, &hour, &keep)
	if err != nil {
		return Settings{}, fmt.Errorf("load backup settings: %w", err)
	}
	return Settings{
		Encrypt:       encrypt == 1,
		PassphraseSet: sealed.Valid && sealed.String != "",
		ScheduleHour:  hour,
		KeepCount:     keep,
	}, nil
}

// SaveSchedule stores the daily backup hour and the keep count. Values outside their range
// return ErrScheduleInvalid and nothing is written.
func SaveSchedule(ctx context.Context, tx *sql.Tx, hour, keepCount int, now time.Time) error {
	if hour < MinScheduleHour || hour > MaxScheduleHour || keepCount < MinKeepCount || keepCount > MaxKeepCount {
		return ErrScheduleInvalid
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE backup_settings SET schedule_hour = ?, keep_count = ?, updated_at = ? WHERE id = 1`,
		hour, keepCount, now.UTC(),
	)
	if err != nil {
		return fmt.Errorf("save backup schedule: %w", err)
	}
	return nil
}

// activePassphrase returns the passphrase to encrypt a new archive with, or "" when encryption is off.
// It fails when encryption is on but the passphrase is missing or cannot be opened.
func activePassphrase(ctx context.Context, db *sql.DB, dbKey string) (string, error) {
	var encrypt int
	var sealed sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT encrypt, passphrase_sealed FROM backup_settings WHERE id = 1`,
	).Scan(&encrypt, &sealed)
	if err != nil {
		return "", fmt.Errorf("load backup settings: %w", err)
	}
	if encrypt != 1 {
		return "", nil
	}
	if !sealed.Valid || sealed.String == "" {
		return "", ErrPassphraseNotSet
	}
	pass, err := openPassphrase(dbKey, sealed.String)
	if err != nil {
		return "", errPassphraseUnreadable
	}
	return pass, nil
}

// SaveEncryption updates the encryption setting inside tx. passphrase nil keeps the stored one,
// "" clears it, and any other value replaces it. Encryption cannot be on without a stored passphrase.
func SaveEncryption(ctx context.Context, tx *sql.Tx, dbKey string, encrypt bool, passphrase *string, now time.Time) error {
	var sealed sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT passphrase_sealed FROM backup_settings WHERE id = 1`,
	).Scan(&sealed); err != nil {
		return fmt.Errorf("load backup passphrase: %w", err)
	}
	current := ""
	if sealed.Valid {
		current = sealed.String
	}

	if passphrase != nil {
		switch {
		case *passphrase == "":
			current = ""
		case len(*passphrase) < MinPassphraseLength:
			return ErrPassphraseTooShort
		default:
			s, err := sealPassphrase(dbKey, *passphrase)
			if err != nil {
				return err
			}
			current = s
		}
	}
	if encrypt && current == "" {
		return ErrPassphraseNotSet
	}

	var stored any
	if current != "" {
		stored = current
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE backup_settings SET encrypt = ?, passphrase_sealed = ?, updated_at = ? WHERE id = 1`,
		boolInt(encrypt), stored, now.UTC(),
	)
	if err != nil {
		return fmt.Errorf("save backup settings: %w", err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func passphraseAEAD(dbKey string) (cipher.AEAD, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(dbKey), nil, []byte(passphraseKeyInfo)), key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealPassphrase returns base64 of nonce followed by ciphertext.
func sealPassphrase(dbKey, passphrase string) (string, error) {
	aead, err := passphraseAEAD(dbKey)
	if err != nil {
		return "", fmt.Errorf("prepare passphrase key: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	sealed := aead.Seal(nonce, nonce, []byte(passphrase), []byte(passphraseAAD))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// openPassphrase decrypts a value from sealPassphrase. Errors never include the passphrase.
func openPassphrase(dbKey, sealed string) (string, error) {
	aead, err := passphraseAEAD(dbKey)
	if err != nil {
		return "", errPassphraseUnreadable
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < aead.NonceSize() {
		return "", errPassphraseUnreadable
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(passphraseAAD))
	if err != nil {
		return "", errPassphraseUnreadable
	}
	return string(plain), nil
}
