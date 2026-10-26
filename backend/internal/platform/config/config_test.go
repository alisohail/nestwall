package config

import (
	"os"
	"testing"
)

func TestLoadConfigMissingRequired(t *testing.T) {
	// Clear env vars to trigger validation error
	os.Unsetenv("DB_KEY")
	os.Unsetenv("SESSION_SECRET")

	_, err := Load()
	if err == nil {
		t.Error("expected error when required environment variables are missing, got nil")
	}
}

func TestLoadConfigSuccess(t *testing.T) {
	os.Setenv("DB_KEY", "db-passphrase")
	os.Setenv("DB_PATH", "/tmp/nestwall.db")
	os.Setenv("SESSION_SECRET", "secret-key")
	defer func() {
		os.Unsetenv("DB_KEY")
		os.Unsetenv("DB_PATH")
		os.Unsetenv("SESSION_SECRET")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error loading valid config: %v", err)
	}

	if cfg.DB.Key != "db-passphrase" {
		t.Errorf("expected DB_KEY db-passphrase, got %s", cfg.DB.Key)
	}
	if cfg.DB.Path != "/tmp/nestwall.db" {
		t.Errorf("expected DB_PATH /tmp/nestwall.db, got %s", cfg.DB.Path)
	}
	if cfg.Secrets.Session != "secret-key" {
		t.Errorf("expected SESSION_SECRET secret-key, got %s", cfg.Secrets.Session)
	}
}
