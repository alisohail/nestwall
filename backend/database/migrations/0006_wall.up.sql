-- Wall displays: devices and pairing codes.
-- Part of the initial schema. Feature files are applied in filename order.

CREATE TABLE wall_devices (
    id                   TEXT PRIMARY KEY,
    family_id            TEXT NOT NULL REFERENCES families (id),
    name                 TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 60),
    credential_hash      TEXT UNIQUE CHECK (credential_hash IS NULL OR length(credential_hash) = 64),
    exit_code_hash       TEXT CHECK (exit_code_hash IS NULL OR length(exit_code_hash) BETWEEN 20 AND 255),
    failed_exit_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_exit_attempts >= 0),
    exit_locked_until    DATETIME,
    show_points          INTEGER NOT NULL DEFAULT 0 CHECK (show_points IN (0, 1)),
    show_weather         INTEGER NOT NULL DEFAULT 1 CHECK (show_weather IN (0, 1)),
    show_tomorrow        INTEGER NOT NULL DEFAULT 1 CHECK (show_tomorrow IN (0, 1)),
    show_pictures        INTEGER NOT NULL DEFAULT 1 CHECK (show_pictures IN (0, 1)),
    show_greeting        INTEGER NOT NULL DEFAULT 1 CHECK (show_greeting IN (0, 1)),
    night_mode           INTEGER NOT NULL DEFAULT 1 CHECK (night_mode IN (0, 1)),
    show_bonus_banner    INTEGER NOT NULL DEFAULT 1 CHECK (show_bonus_banner IN (0, 1)),
    paired_at            DATETIME,
    last_seen_at         DATETIME,
    revoked_at           DATETIME,
    created_by           TEXT NOT NULL REFERENCES users (id),
    created_at           DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at           DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    CHECK ((credential_hash IS NULL) = (paired_at IS NULL)),
    CHECK (credential_hash IS NULL OR exit_code_hash IS NOT NULL)
);

CREATE TABLE wall_pairing_codes (
    id          TEXT PRIMARY KEY,
    family_id   TEXT NOT NULL REFERENCES families (id),
    device_id   TEXT NOT NULL,
    code_hash   TEXT NOT NULL UNIQUE CHECK (length(code_hash) = 64),
    expires_at  DATETIME NOT NULL,
    used_at     DATETIME,
    created_by  TEXT NOT NULL REFERENCES users (id),
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    FOREIGN KEY (family_id, device_id) REFERENCES wall_devices (family_id, id) ON DELETE CASCADE
);

CREATE INDEX wall_pairing_codes_by_device ON wall_pairing_codes (device_id) WHERE used_at IS NULL;
