package calendar

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

// Links to external calendars carry secret tokens, so they are stored sealed with AES-256-GCM. The
// key is derived from DB_KEY, so no extra setting is needed. The import id is bound as associated
// data, so a sealed link cannot be moved to another import.

const linkKeyInfo = "nestwall calendar link v1"

var errLinkSeal = errors.New("calendar link cannot be opened")

func linkAEAD(dbKey string) (cipher.AEAD, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(dbKey), nil, []byte(linkKeyInfo)), key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealLink encrypts a link for storage. The result is base64 of nonce followed by ciphertext.
func sealLink(dbKey, importID, link string) (string, error) {
	aead, err := linkAEAD(dbKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(link), []byte(importID))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// openLink decrypts a stored link. Errors never include the link.
func openLink(dbKey, importID, sealed string) (string, error) {
	aead, err := linkAEAD(dbKey)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < aead.NonceSize() {
		return "", errLinkSeal
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(importID))
	if err != nil {
		return "", errLinkSeal
	}
	return string(plain), nil
}
