package config

import (
	"fmt"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Config holds the configuration for the application.
type Config struct {
	DB struct {
		// Path is the SQLCipher database file. The default is for local runs;
		// the container sets DB_PATH.
		Path string `envconfig:"DB_PATH" default:"database/nestwall.db"`
		// Key is the passphrase that encrypts the database file.
		Key string `envconfig:"DB_KEY" required:"true"`
	}
	Backup struct {
		// Dir holds the scheduled and manual backup archives. The container sets BACKUP_DIR.
		Dir string `envconfig:"BACKUP_DIR" default:"database/backups"`
	}
	API struct {
		Port string `envconfig:"API_PORT" default:"8080"`
	}
	App struct {
		// URL is the public address of the app. Reset links in emails point here.
		URL string `envconfig:"APP_URL" default:"http://localhost:8080"`
	}
	Secrets struct {
		Session string `envconfig:"SESSION_SECRET" required:"true"`
	}
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	// It's not an error if the .env file doesn't exist
	_ = godotenv.Load()

	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("failed to process config: %w", err)
	}

	return &cfg, nil
}
