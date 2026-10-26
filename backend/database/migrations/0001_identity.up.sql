-- Identity: families, users, sessions, members, avatars, audit log, app settings.
-- Part of the initial schema. Feature files are applied in filename order.

CREATE TABLE api_tokens (
    id            TEXT PRIMARY KEY,
    family_id     TEXT NOT NULL REFERENCES families (id),
    name          TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 60),
    token_hash    TEXT NOT NULL UNIQUE CHECK (length(token_hash) = 64),
    token_prefix  TEXT NOT NULL CHECK (length(token_prefix) = 8),
    token_suffix  TEXT NOT NULL CHECK (length(token_suffix) = 2),
    scopes        TEXT NOT NULL,
    expires_at    DATETIME NOT NULL,
    last_used_at  DATETIME,
    revoked_at    DATETIME,
    created_by    TEXT NOT NULL REFERENCES users (id),
    created_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE TABLE "app_settings" (
    family_id     TEXT NOT NULL REFERENCES families(id),
    setting_key   TEXT NOT NULL CHECK (setting_key IN (
                      'defaults.points',
                      'defaults.approval_required',
                      'defaults.expires_after_minutes',
                      'defaults.missed_after_value',
                      'defaults.missed_after_unit',
                      'defaults.penalty_value',
                      'defaults.penalty_unit',
                      'part_of_day.morning_start',
                      'part_of_day.after_school_start',
                      'part_of_day.evening_start',
                      'points.allow_negative_balance',
                      'bonus_defaults.points',
                      'bonus_defaults.claim_mode',
                      'bonus_defaults.show_from',
                      'bonus_defaults.ends',
                      'bonus_defaults.approval_required',
                      'rewards.default_approval_required')),
    setting_value TEXT NOT NULL CHECK (length(trim(setting_value)) > 0),
    updated_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    PRIMARY KEY (family_id, setting_key)
);

CREATE TABLE audit_logs (
    id          TEXT PRIMARY KEY,
    family_id   TEXT NOT NULL REFERENCES families(id),
    actor_id    TEXT NOT NULL REFERENCES users(id),
    action      TEXT NOT NULL,
    target_type TEXT,
    target_id   TEXT,
    metadata    TEXT,
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE TABLE families (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    timezone       TEXT NOT NULL DEFAULT 'UTC',
    latitude       REAL,
    longitude      REAL,
    settings       TEXT NOT NULL DEFAULT '{}',
    singleton_lock INTEGER DEFAULT 1 UNIQUE,
    created_at     DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at     DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
, members_can_change_avatar INTEGER NOT NULL DEFAULT 1 CHECK (members_can_change_avatar IN (0, 1)), show_avatars_on_wall INTEGER NOT NULL DEFAULT 1 CHECK (show_avatars_on_wall IN (0, 1)), task_settings_version INTEGER NOT NULL DEFAULT 1 CHECK (task_settings_version >= 1), location_name TEXT CHECK (location_name IS NULL OR length(trim(location_name)) BETWEEN 1 AND 80), temperature_unit TEXT NOT NULL DEFAULT 'c' CHECK (temperature_unit IN ('c', 'f')), weather_animation TEXT NOT NULL DEFAULT 'calm' CHECK (weather_animation IN ('calm', 'live')));

CREATE TABLE family_members (
    id          TEXT PRIMARY KEY,
    family_id   TEXT NOT NULL REFERENCES families(id),
    user_id     TEXT NOT NULL REFERENCES users(id),
    role_id     TEXT NOT NULL REFERENCES roles(id),
    status      TEXT NOT NULL DEFAULT 'active',
    avatar_id   TEXT,
    colour      TEXT NOT NULL,
    pin_hash    TEXT,
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
, permissions_version INTEGER NOT NULL DEFAULT 1 CHECK (permissions_version >= 1));

CREATE TABLE member_avatars (
    id               TEXT PRIMARY KEY,
    family_id        TEXT NOT NULL REFERENCES families(id),
    family_member_id TEXT NOT NULL REFERENCES family_members(id) ON DELETE CASCADE,
    created_at       DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE TABLE password_reset_tokens (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMP NOT NULL,
    used_at     TIMESTAMP,
    created_at  TIMESTAMP NOT NULL
);

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    hashed_token TEXT NOT NULL UNIQUE,
    user_id      TEXT NOT NULL REFERENCES users(id),
    expiry       DATETIME NOT NULL,
    revoked_at   DATETIME,
    user_agent   TEXT,
    created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL UNIQUE,
    email         TEXT,
    password_hash TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    is_child      INTEGER NOT NULL DEFAULT 0 CHECK (is_child IN (0, 1)),
    created_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
, must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1)), date_of_birth TEXT
    CHECK (date_of_birth IS NULL OR date_of_birth GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'), gender TEXT
    CHECK (gender IS NULL OR gender IN ('female', 'male', 'prefer_not_to_say')), theme TEXT NOT NULL DEFAULT 'light' CHECK (theme IN ('system', 'light', 'dark')));

CREATE INDEX api_tokens_by_family ON api_tokens (family_id) WHERE revoked_at IS NULL;

CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at DESC);

CREATE INDEX idx_audit_logs_family_id ON audit_logs(family_id);

CREATE INDEX idx_family_members_family_id ON family_members(family_id);

CREATE UNIQUE INDEX idx_family_members_family_id_id ON family_members(family_id, id);

CREATE INDEX idx_family_members_user_id ON family_members(user_id);

CREATE INDEX idx_member_avatars_member ON member_avatars(family_member_id);

CREATE INDEX idx_sessions_user_id ON sessions(user_id);

CREATE INDEX password_reset_tokens_user ON password_reset_tokens (user_id);

CREATE UNIQUE INDEX users_email_unique ON users (LOWER(email)) WHERE email IS NOT NULL;
