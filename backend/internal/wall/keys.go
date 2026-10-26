package wall

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
)

// keyAlphabet leaves out I, L, O and the digits 0 and 1, so a key read aloud or copied by eye is unambiguous.
const keyAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

const (
	keyLength = 8
	// keyByteLimit is the largest multiple of len(keyAlphabet) that fits in a byte. Bytes at or above it
	// are rejected so every symbol is equally likely.
	keyByteLimit = 248
	// credentialBytes is the size of the random device credential before encoding.
	credentialBytes = 32
)

// newPairingKey returns a random pairing key of keyLength symbols from keyAlphabet.
func newPairingKey() (string, error) {
	key := make([]byte, 0, keyLength)
	buf := make([]byte, 1)
	for len(key) < keyLength {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if int(buf[0]) >= keyByteLimit {
			continue
		}
		key = append(key, keyAlphabet[int(buf[0])%len(keyAlphabet)])
	}
	return string(key), nil
}

// formatKey splits a pairing key into two halves for display, for example K7M4-9QXD.
func formatKey(key string) string {
	return key[:keyLength/2] + "-" + key[keyLength/2:]
}

// normalizeKey removes dashes and spaces and uppercases the key, so the tablet accepts what a person types.
func normalizeKey(input string) string {
	cleaned := strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(input))
	return strings.ToUpper(cleaned)
}

// validKey reports whether a normalised key has the right length and only alphabet symbols.
func validKey(key string) bool {
	if len(key) != keyLength {
		return false
	}
	for i := 0; i < len(key); i++ {
		if strings.IndexByte(keyAlphabet, key[i]) < 0 {
			return false
		}
	}
	return true
}

// newCredential returns a random device credential. The tablet keeps it in a cookie; only its hash is stored.
func newCredential() (string, error) {
	raw := make([]byte, credentialBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
