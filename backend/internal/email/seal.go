package email

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"

	"golang.org/x/crypto/hkdf"
)

// The SMTP password is stored sealed with AES-256-GCM. The key is derived from DB_KEY, so no
// extra setting is needed. The family id is bound as associated data, so a sealed password
// cannot be moved to another family.

const sealKeyInfo = "nestwall smtp password v1"

var errSeal = errors.New("smtp password cannot be opened")

func sealAEAD(dbKey string) (cipher.AEAD, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(dbKey), nil, []byte(sealKeyInfo)), key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealSecret encrypts a secret for storage. The result is base64 of nonce followed by ciphertext.
func sealSecret(dbKey, familyID, secret string) (string, error) {
	aead, err := sealAEAD(dbKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(secret), []byte(familyID))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// openSecret decrypts a stored secret. Errors never include the secret.
func openSecret(dbKey, familyID, sealed string) (string, error) {
	aead, err := sealAEAD(dbKey)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < aead.NonceSize() {
		return "", errSeal
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(familyID))
	if err != nil {
		return "", errSeal
	}
	return string(plain), nil
}
