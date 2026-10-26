-- Calendar: local events, ICS imports and external sources.
-- Part of the initial schema. Feature files are applied in filename order.

CREATE TABLE calendar_event_exceptions (
    id               TEXT PRIMARY KEY,
    family_id        TEXT NOT NULL REFERENCES families (id),
    event_id         TEXT NOT NULL,
    occurrence_date  TEXT NOT NULL CHECK (occurrence_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    kind             TEXT NOT NULL CHECK (kind IN ('cancelled', 'edited')),
    title            TEXT CHECK (title IS NULL OR length(trim(title)) BETWEEN 1 AND 200),
    description      TEXT CHECK (description IS NULL OR length(description) <= 2000),
    location         TEXT CHECK (location IS NULL OR length(location) <= 200),
    starts_at        DATETIME,
    ends_at          DATETIME,
    created_at       DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at       DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (event_id, occurrence_date),
    CHECK (
        (kind = 'cancelled' AND title IS NULL AND description IS NULL AND location IS NULL
            AND starts_at IS NULL AND ends_at IS NULL)
        OR
        (kind = 'edited' AND title IS NOT NULL AND starts_at IS NOT NULL AND ends_at IS NOT NULL
            AND starts_at < ends_at)
    ),
    FOREIGN KEY (family_id, event_id) REFERENCES calendar_events (family_id, id) ON DELETE CASCADE
);

CREATE TABLE calendar_event_members (
    id                TEXT PRIMARY KEY,
    family_id         TEXT NOT NULL REFERENCES families (id),
    event_id          TEXT NOT NULL,
    family_member_id  TEXT NOT NULL,
    created_at        DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (event_id, family_member_id),
    FOREIGN KEY (family_id, event_id) REFERENCES calendar_events (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE calendar_events (
    id               TEXT PRIMARY KEY,
    family_id        TEXT NOT NULL REFERENCES families (id),
    source_id        TEXT NOT NULL,
    title            TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 200),
    description      TEXT CHECK (description IS NULL OR length(description) <= 2000),
    location         TEXT CHECK (location IS NULL OR length(location) <= 200),
    all_day          INTEGER NOT NULL CHECK (all_day IN (0, 1)),
    time_zone        TEXT NOT NULL CHECK (length(time_zone) BETWEEN 1 AND 64),
    start_date       TEXT NOT NULL CHECK (start_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    start_time       TEXT CHECK (start_time IS NULL OR start_time GLOB '[0-2][0-9]:[0-5][0-9]'),
    duration_minutes INTEGER CHECK (duration_minutes IS NULL OR duration_minutes BETWEEN 1 AND 525600),
    span_days        INTEGER CHECK (span_days IS NULL OR span_days BETWEEN 0 AND 3650),
    starts_at        DATETIME NOT NULL,
    ends_at          DATETIME NOT NULL,
    recurrence_rule  TEXT CHECK (recurrence_rule IS NULL OR length(recurrence_rule) <= 500),
    version          INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by       TEXT NOT NULL REFERENCES users (id),
    created_at       DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at       DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    import_id        TEXT REFERENCES calendar_imports (id) ON DELETE CASCADE,
    from_feed        INTEGER NOT NULL DEFAULT 0 CHECK (from_feed IN (0, 1)),
    UNIQUE (family_id, id),
    CHECK (starts_at < ends_at),
    CHECK (
        (all_day = 1 AND start_time IS NULL AND duration_minutes IS NULL AND span_days IS NOT NULL)
        OR
        (all_day = 0 AND start_time IS NOT NULL AND duration_minutes IS NOT NULL AND span_days IS NULL)
    ),
    FOREIGN KEY (family_id, source_id) REFERENCES calendar_sources (family_id, id)
);

-- Reminders are minutes before each occurrence starts. created_at is when the reminder was armed:
-- an occurrence whose reminder time falls before it is never reminded.
CREATE TABLE calendar_event_reminders (
    id           TEXT PRIMARY KEY,
    family_id    TEXT NOT NULL REFERENCES families (id),
    event_id     TEXT NOT NULL,
    minutes      INTEGER NOT NULL CHECK (minutes BETWEEN 0 AND 40320),
    created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (event_id, minutes),
    FOREIGN KEY (family_id, event_id) REFERENCES calendar_events (family_id, id) ON DELETE CASCADE
);

-- One row per reminder that has been sent for one occurrence, keyed by the occurrence start.
CREATE TABLE calendar_reminder_deliveries (
    family_id      TEXT NOT NULL REFERENCES families (id),
    event_id       TEXT NOT NULL,
    minutes        INTEGER NOT NULL CHECK (minutes BETWEEN 0 AND 40320),
    occurrence_at  DATETIME NOT NULL,
    sent_at        DATETIME NOT NULL,
    PRIMARY KEY (event_id, minutes, occurrence_at),
    FOREIGN KEY (family_id, event_id) REFERENCES calendar_events (family_id, id) ON DELETE CASCADE
);

CREATE TABLE calendar_import_members (
    id                TEXT PRIMARY KEY,
    family_id         TEXT NOT NULL REFERENCES families (id),
    import_id         TEXT NOT NULL,
    family_member_id  TEXT NOT NULL,
    created_at        DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (import_id, family_member_id),
    FOREIGN KEY (family_id, import_id) REFERENCES calendar_imports (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE calendar_imports (
    id             TEXT PRIMARY KEY,
    family_id      TEXT NOT NULL REFERENCES families (id),
    name           TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
    file_name      TEXT CHECK (file_name IS NULL OR length(file_name) <= 255),
    skipped_count  INTEGER NOT NULL DEFAULT 0 CHECK (skipped_count >= 0),
    created_by     TEXT NOT NULL REFERENCES users (id),
    created_at     DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at     DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')), colour TEXT NOT NULL DEFAULT '#0D9488'
    CHECK (colour GLOB '#[0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f]'), audience TEXT NOT NULL DEFAULT 'everyone'
    CHECK (audience IN ('everyone', 'specific', 'parents')), show_on_wall INTEGER NOT NULL DEFAULT 1
    CHECK (show_on_wall IN (0, 1)), source_url_sealed TEXT CHECK (source_url_sealed IS NULL OR length(source_url_sealed) <= 4096), source_etag TEXT CHECK (source_etag IS NULL OR length(source_etag) <= 255), last_synced_at DATETIME, last_sync_error TEXT CHECK (last_sync_error IS NULL OR length(last_sync_error) <= 64), refresh_minutes INTEGER NOT NULL DEFAULT 30 CHECK (refresh_minutes IN (15, 30, 60, 360, 1440)), paused INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)), last_attempt_at DATETIME, duplicate_count INTEGER NOT NULL DEFAULT 0 CHECK (duplicate_count >= 0), parser_version INTEGER NOT NULL DEFAULT 0 CHECK (parser_version >= 0),
    UNIQUE (family_id, id)
);

CREATE TABLE calendar_sources (
    id          TEXT PRIMARY KEY,
    family_id   TEXT NOT NULL REFERENCES families (id),
    kind        TEXT NOT NULL CHECK (kind IN ('local')),
    name        TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
    colour      TEXT CHECK (colour IS NULL OR colour GLOB '#[0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f]'),
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id)
);

CREATE INDEX calendar_event_members_by_member ON calendar_event_members (family_id, family_member_id);

CREATE INDEX calendar_events_by_import ON calendar_events (import_id) WHERE import_id IS NOT NULL;

CREATE INDEX calendar_import_members_by_member ON calendar_import_members (family_id, family_member_id);
