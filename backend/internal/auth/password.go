package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Configuration for Argon2id password hashing.
const (
	// Argon2id parameters - OWASP recommendation
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// temporaryAlphabet leaves out look-alike characters (0, O, 1, l, I) so that a
// temporary password can be read aloud or copied by hand.
const temporaryAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"

// temporaryPasswordLength is the length of generated temporary passwords.
const temporaryPasswordLength = 16

// GenerateTemporaryPassword returns a random password that a parent shows once
// to a family member. The user must replace it at the next sign-in.
func GenerateTemporaryPassword() (string, error) {
	out := make([]byte, temporaryPasswordLength)
	buf := make([]byte, temporaryPasswordLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate temporary password: %w", err)
	}
	for i, b := range buf {
		out[i] = temporaryAlphabet[int(b)%len(temporaryAlphabet)]
	}
	return string(out), nil
}

// HashPassword hashes a password using Argon2id.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	// Encode as hex: salt:hash
	encoded := fmt.Sprintf("%s:%s", hex.EncodeToString(salt), hex.EncodeToString(hash))
	return encoded, nil
}

// VerifyPassword checks if a password matches a hash.
func VerifyPassword(password, encodedHash string) bool {
	parts := splitTwo(encodedHash, ":")
	if len(parts) != 2 {
		return false
	}

	salt, err := hex.DecodeString(parts[0])
	if err != nil {
		return false
	}

	hash, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	computedHash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return subtle.ConstantTimeCompare(hash, computedHash) == 1
}

// splitTwo splits a string on the first occurrence of sep.
func splitTwo(s, sep string) []string {
	idx := strings.Index(s, sep)
	if idx == -1 {
		return nil
	}
	return []string{s[:idx], s[idx+len(sep):]}
}
