package wall

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestPairingKeyUsesOnlyAlphabetSymbols(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		key, err := newPairingKey()
		if err != nil {
			t.Fatalf("newPairingKey: %v", err)
		}
		if len(key) != keyLength {
			t.Fatalf("key %q has length %d, want %d", key, len(key), keyLength)
		}
		if !validKey(key) {
			t.Fatalf("key %q has a symbol outside the alphabet", key)
		}
		seen[key] = true
	}
	if len(seen) < 495 {
		t.Fatalf("only %d distinct keys in 500 draws, randomness looks broken", len(seen))
	}
}

func TestFormatKeySplitsInHalves(t *testing.T) {
	if got := formatKey("K7M49QXD"); got != "K7M4-9QXD" {
		t.Fatalf("formatKey = %q, want K7M4-9QXD", got)
	}
}

func TestNormalizeKeyAcceptsTypedInput(t *testing.T) {
	cases := map[string]string{
		"K7M4-9QXD":   "K7M49QXD",
		"k7m4-9qxd":   "K7M49QXD",
		"  k7m49qxd ": "K7M49QXD",
		"K7M4 9QXD":   "K7M49QXD",
	}
	for input, want := range cases {
		got := normalizeKey(input)
		if got != want {
			t.Errorf("normalizeKey(%q) = %q, want %q", input, got, want)
		}
		if !validKey(got) {
			t.Errorf("normalizeKey(%q) = %q is not a valid key", input, got)
		}
	}
}

func TestValidKeyRejectsBadInput(t *testing.T) {
	for _, key := range []string{"", "K7M49QX", "K7M49QXDD", "K7M49QXI", "K7M49QX0", "K7M49QX1", "K7M49QXL"} {
		if validKey(key) {
			t.Errorf("validKey(%q) = true, want false", key)
		}
	}
}

func TestNewCredentialIsLongAndRandom(t *testing.T) {
	first, err := newCredential()
	if err != nil {
		t.Fatalf("newCredential: %v", err)
	}
	second, err := newCredential()
	if err != nil {
		t.Fatalf("newCredential: %v", err)
	}
	if first == second {
		t.Fatal("two credentials are equal")
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("credential is not base64url: %v", err)
	}
	if len(raw) != credentialBytes {
		t.Fatalf("credential decodes to %d bytes, want %d", len(raw), credentialBytes)
	}
	if strings.ContainsAny(first, "+/=") {
		t.Fatalf("credential %q is not URL-safe", first)
	}
}
