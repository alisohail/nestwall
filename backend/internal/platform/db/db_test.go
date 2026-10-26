package db

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenWrongKeyFails checks that the database file is encrypted and that
// only the key it was created with can open it (PBI M1-03).
func TestOpenWrongKeyFails(t *testing.T) {
	ctx := context.Background()
	filePath := filepath.Join(t.TempDir(), "nestwall.db")

	conn, err := Open(ctx, filePath, "right-key-passphrase")
	if err != nil {
		t.Fatalf("open with right key: %v", err)
	}
	conn.Close()

	header, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read database file: %v", err)
	}
	if bytes.HasPrefix(header, []byte("SQLite format 3\x00")) {
		t.Fatal("database file is plaintext SQLite, want it encrypted")
	}

	if _, err := Open(ctx, filePath, "wrong-key-passphrase"); err == nil {
		t.Fatal("open with wrong key succeeded, want error")
	}
}
