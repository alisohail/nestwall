package calendar

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSealedLinkRoundTrip(t *testing.T) {
	const link = "https://calendar.example.com/feed.ics?token=abc123"
	sealed, err := sealLink(testDBKey, "import-1", link)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(sealed, "abc123") || strings.Contains(sealed, "calendar.example.com") {
		t.Fatalf("sealed value contains plaintext: %q", sealed)
	}
	got, err := openLink(testDBKey, "import-1", sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got != link {
		t.Fatalf("round trip = %q, want %q", got, link)
	}
}

func TestSealedLinkRejectsWrongImportKeyOrTampering(t *testing.T) {
	sealed, err := sealLink(testDBKey, "import-1", "https://example.com/a.ics")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := openLink(testDBKey, "import-2", sealed); err == nil {
		t.Error("link opened under another import id")
	}
	if _, err := openLink("another-db-key", "import-1", sealed); err == nil {
		t.Error("link opened under another DB key")
	}
	if _, err := openLink(testDBKey, "import-1", "not-base64!!"); err == nil {
		t.Error("garbage opened")
	}
	// Flip a byte in the raw ciphertext so authentication fails. Editing the
	// base64 text can hit padding bits that decode to the same bytes.
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw[len(raw)-1] ^= 0x01
	tampered := base64.StdEncoding.EncodeToString(raw)
	if _, err := openLink(testDBKey, "import-1", tampered); err == nil {
		t.Error("tampered link opened")
	}
}

func TestSealedLinkUsesFreshNonce(t *testing.T) {
	a, _ := sealLink(testDBKey, "import-1", "https://example.com/a.ics")
	b, _ := sealLink(testDBKey, "import-1", "https://example.com/a.ics")
	if a == b {
		t.Fatal("two seals of the same link are identical")
	}
}
