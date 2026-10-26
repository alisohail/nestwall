-- Backups: settings and run history.
-- Part of the initial schema. Feature files are applied in filename order.

CREATE TABLE "backup_runs" (
    id             TEXT PRIMARY KEY,
    kind           TEXT NOT NULL CHECK (kind IN ('scheduled', 'manual', 'before_reset')),
    status         TEXT NOT NULL CHECK (status IN ('running', 'ok', 'failed')),
    file_name      TEXT,
    size_bytes     INTEGER CHECK (size_bytes IS NULL OR size_bytes >= 0),
    encrypted      INTEGER NOT NULL DEFAULT 0 CHECK (encrypted IN (0, 1)),
    verified       INTEGER NOT NULL DEFAULT 0 CHECK (verified IN (0, 1)),
    counts_json    TEXT,
    error_message  TEXT,
    started_at     DATETIME NOT NULL,
    finished_at    DATETIME,
    created_at     DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE TABLE "backup_settings" (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    encrypt            INTEGER NOT NULL DEFAULT 0 CHECK (encrypt IN (0, 1)),
    passphrase_sealed  TEXT,
    schedule_hour      INTEGER NOT NULL DEFAULT 2 CHECK (schedule_hour BETWEEN 0 AND 23),
    keep_count         INTEGER NOT NULL DEFAULT 6 CHECK (keep_count BETWEEN 1 AND 60),
    updated_at         DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE INDEX backup_runs_by_started ON backup_runs (started_at DESC);

-- Diagnostics events: recent server errors, calendar sync failures and email failures.
-- Each row stores a fixed code only, never error text from calendars, mail servers or requests.
-- The app keeps only the newest rows (see internal/diagnostics).
CREATE TABLE "diagnostic_events" (
    id          TEXT PRIMARY KEY,
    family_id   TEXT REFERENCES families (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('server_error', 'calendar_sync', 'email')),
    code        TEXT NOT NULL,
    status      INTEGER,
    path        TEXT,
    created_at  DATETIME NOT NULL
);

CREATE INDEX diagnostic_events_by_created ON diagnostic_events (created_at DESC);

INSERT INTO "backup_settings" VALUES(1,0,NULL,2,6,'2026-10-23 13:20:33.792+00:00');
